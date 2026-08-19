package httpsource

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Logger découple le moteur du logger du SDK (import cycle) et rend les tests muets.
type Logger interface {
	Infof(format string, args ...any)
	Warnf(format string, args ...any)
}

// nopLogger : défaut silencieux. Un moteur sans logger doit fonctionner, pas paniquer.
type nopLogger struct{}

func (nopLogger) Infof(string, ...any) {}
func (nopLogger) Warnf(string, ...any) {}

// Engine exécute une Spec. Sans état entre deux Fetch : réutilisable et sûr à garder
// en variable de package dans un proc.
type Engine struct {
	client *http.Client
	logger Logger
	sleep  func(context.Context, time.Duration) error
}

// Option configure l'Engine.
type Option func(*Engine)

// #region WithHTTPClient
func WithHTTPClient(c *http.Client) Option {
	return func(e *Engine) {
		if c != nil {
			e.client = c
		}
	}
}

// #endregion

// #region WithLogger
func WithLogger(l Logger) Option {
	return func(e *Engine) {
		if l != nil {
			e.logger = l
		}
	}
}

// #endregion

// #region WithSleeper
// WithSleeper remplace l'attente entre deux tentatives. Indispensable aux tests : sans
// ça, valider un scénario "429 puis 200 avec Retry-After: 60" prendrait une minute et
// ne serait jamais lancé en CI.
func WithSleeper(f func(context.Context, time.Duration) error) Option {
	return func(e *Engine) {
		if f != nil {
			e.sleep = f
		}
	}
}

// #endregion

// #region New
func New(opts ...Option) *Engine {
	e := &Engine{
		logger: nopLogger{},
		sleep:  realSleep,
		client: &http.Client{
			Transport: &http.Transport{
				Proxy: http.ProxyFromEnvironment,
				DialContext: (&net.Dialer{
					Timeout:   10 * time.Second,
					KeepAlive: 120 * time.Second,
				}).DialContext,
				MaxIdleConns:        100,
				MaxIdleConnsPerHost: 10,
				IdleConnTimeout:     90 * time.Second,
			},
		},
	}
	for _, opt := range opts {
		opt(e)
	}
	return e
}

// #endregion

// #region realSleep
func realSleep(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return nil
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(d):
		return nil
	}
}

// #endregion

// Stats résume une collecte. Sert au log de fin et aux assertions de test.
type Stats struct {
	Pages    int
	Rows     int
	Attempts int
	Waited   time.Duration
}

// EmitFunc reçoit chaque ligne. Renvoyer ErrStop interrompt proprement la collecte
// (utilisé par test-query pour ne prendre que les N premières lignes).
type EmitFunc func(row map[string]any) error

// #region Fetch
// Fetch exécute la spec et pousse chaque ligne dans emit.
//
// STREAMING et non accumulation : une API qui renvoie 200 000 lignes sur 400 pages ne
// doit pas tenir en RAM dans un container worker. C'est aussi ce qui permet à
// processor-v2 de recevoir les lignes au fil de l'eau.
func (e *Engine) Fetch(ctx context.Context, spec *Spec, vars Vars, emit EmitFunc) (Stats, error) {
	var stats Stats

	if spec == nil {
		return stats, newErr(KindInvalidSpec, 0, nil, "no spec provided")
	}
	if emit == nil {
		return stats, newErr(KindInvalidSpec, 0, nil, "no emit callback provided")
	}

	redactor := NewRedactor(vars.Credentials)
	auth := newAuthenticator(spec, vars, e.client, redactor)
	pager := newPaginator(&spec.Pagination)

	// Timeout par requête et non global : sur une pagination longue, un timeout global
	// couperait au milieu et laisserait la date à moitié chargée.
	client := *e.client
	client.Timeout = time.Duration(spec.Source.TimeoutSeconds) * time.Second
	auth.client = &client

	e.logger.Infof("httpsource: %s %s (auth=%s, pagination=%s, format=%s)",
		spec.Source.Method, redactURL(spec.Source.URL), describeAuth(spec), spec.Pagination.Type, spec.Records.Format)

	for {
		if stats.Pages >= spec.Pagination.MaxPages {
			// Warning et pas erreur : les lignes déjà émises sont valides, et une
			// erreur ferait rejouer toute la date au run suivant.
			e.logger.Warnf("httpsource: stopped at the %d-page safety cap (pagination.maxPages) — data may be incomplete", spec.Pagination.MaxPages)
			break
		}

		body, parsed, header, attempts, waited, err := e.fetchPage(ctx, &client, spec, vars, auth, pager, redactor)
		stats.Attempts += attempts
		stats.Waited += waited
		if err != nil {
			return stats, err
		}
		stats.Pages++

		rows, err := extractRecords(body, spec, vars)
		if err != nil {
			return stats, redactor.Err(err)
		}

		for _, row := range rows {
			if emitErr := emit(row); emitErr != nil {
				if KindOf(emitErr) == KindStopped {
					stats.Rows += len(rows)
					return stats, nil
				}
				return stats, emitErr
			}
			stats.Rows++
		}

		if !pager.advance(parsed, len(rows), header) {
			break
		}
	}

	e.logger.Infof("httpsource: done — %d row(s) over %d page(s), %d HTTP attempt(s), %s waited",
		stats.Rows, stats.Pages, stats.Attempts, stats.Waited)

	return stats, nil
}

// #endregion

// #region fetchPage
// fetchPage récupère UNE page, retries compris.
func (e *Engine) fetchPage(
	ctx context.Context,
	client *http.Client,
	spec *Spec,
	vars Vars,
	auth *authenticator,
	pager *paginator,
	redactor *Redactor,
) (body []byte, parsed any, header http.Header, attempts int, waited time.Duration, err error) {

	// tokenRetried borne le renouvellement de token à UNE reprise par page : un 401
	// persistant après un token neuf est une vraie erreur d'auth, pas un token expiré.
	tokenRetried := false

	for attempt := 0; attempt < spec.Retry.MaxAttempts; attempt++ {
		attempts++

		req, buildErr := e.buildRequest(ctx, spec, vars, pager)
		if buildErr != nil {
			return nil, nil, nil, attempts, waited, redactor.Err(buildErr)
		}
		if authErr := auth.apply(ctx, req); authErr != nil {
			return nil, nil, nil, attempts, waited, redactor.Err(authErr)
		}

		resp, doErr := client.Do(req)
		if doErr != nil {
			// Erreur transport (DNS, TCP, TLS, timeout). Toujours retentable : c'est
			// très majoritairement passager.
			if attempt == spec.Retry.MaxAttempts-1 {
				return nil, nil, nil, attempts, waited, redactor.Err(
					newErr(KindUnavailable, 0, doErr, "request failed after %d attempt(s) (%s)", attempts, redactURL(req.URL.String())))
			}
			d := waitForBackoff(spec.Retry.OnNetwork, attempt)
			e.logger.Warnf("httpsource: network error (attempt %d/%d), retrying in %s: %v",
				attempt+1, spec.Retry.MaxAttempts, d, redactor.String(doErr.Error()))
			if sleepErr := e.sleep(ctx, d); sleepErr != nil {
				return nil, nil, nil, attempts, waited, sleepErr
			}
			waited += d
			continue
		}

		respBody, readErr := io.ReadAll(resp.Body)
		resp.Body.Close()
		if readErr != nil {
			if attempt == spec.Retry.MaxAttempts-1 {
				return nil, nil, nil, attempts, waited, redactor.Err(
					newErr(KindUnavailable, resp.StatusCode, readErr, "cannot read the response body"))
			}
			d := waitForBackoff(spec.Retry.OnNetwork, attempt)
			if sleepErr := e.sleep(ctx, d); sleepErr != nil {
				return nil, nil, nil, attempts, waited, sleepErr
			}
			waited += d
			continue
		}

		outcome, kind := classify(resp.StatusCode)

		switch outcome {
		case outcomeSuccess:
			if spec.Records.Format == FormatJSON || spec.Pagination.needsParsedBody() {
				if unmarshalErr := json.Unmarshal(respBody, &parsed); unmarshalErr != nil && spec.Records.Format == FormatJSON {
					return nil, nil, nil, attempts, waited, redactor.Err(
						newErr(KindInvalidData, resp.StatusCode, unmarshalErr,
							"response is not valid JSON (first bytes: %q)", truncate(string(respBody), 120)))
				}
			}
			return respBody, parsed, resp.Header, attempts, waited, nil

		case outcomeFatal:
			// 401/403 avec un token OAuth : le token a pu être révoqué avant son
			// expiration annoncée. Un seul renouvellement + une seule reprise.
			if kind == KindAuth && auth.usesToken() && !tokenRetried {
				tokenRetried = true
				auth.invalidateToken()
				e.logger.Warnf("httpsource: HTTP %d, refreshing the OAuth2 token and retrying once", resp.StatusCode)
				continue
			}
			return nil, nil, nil, attempts, waited, redactor.Err(
				newErr(kind, resp.StatusCode, nil, "%s", truncate(string(respBody), 300)))

		case outcomeRetry:
			var d time.Duration
			if kind == KindRateLimit {
				d = waitFor429(spec.Retry.On429, resp.Header, respBody, attempt)
			} else {
				d = waitForBackoff(spec.Retry.On5xx, attempt)
			}

			if attempt == spec.Retry.MaxAttempts-1 {
				return nil, nil, nil, attempts, waited, redactor.Err(
					newErr(kind, resp.StatusCode, nil,
						"still failing after %d attempt(s): %s", attempts, truncate(string(respBody), 300)))
			}

			e.logger.Warnf("httpsource: HTTP %d (attempt %d/%d), retrying in %s",
				resp.StatusCode, attempt+1, spec.Retry.MaxAttempts, d)
			if sleepErr := e.sleep(ctx, d); sleepErr != nil {
				return nil, nil, nil, attempts, waited, sleepErr
			}
			waited += d
		}
	}

	return nil, nil, nil, attempts, waited, redactor.Err(
		newErr(KindUnavailable, 0, nil, "exhausted %d attempt(s) without a usable response", attempts))
}

// #endregion

// #region needsParsedBody
// needsParsedBody indique si la pagination a besoin du corps décodé. Évite de parser
// du JSON pour rien quand on collecte du CSV sans pagination par curseur.
func (p Pagination) needsParsedBody() bool {
	switch p.Type {
	case PageCursor, PageNextURL:
		return true
	case PagePage:
		return p.StopWhen == "totalPages"
	default:
		return false
	}
}

// #endregion

// #region buildRequest
func (e *Engine) buildRequest(ctx context.Context, spec *Spec, vars Vars, pager *paginator) (*http.Request, error) {
	target := pager.overrideURL()

	if target == "" {
		rendered, err := Render(spec.Source.URL, vars)
		if err != nil {
			return nil, err
		}

		parsedURL, err := url.Parse(rendered)
		if err != nil {
			return nil, newErr(KindInvalidSpec, 0, err, "source.url is not a valid URL after templating")
		}

		query, err := RenderMap(spec.Source.Query, vars)
		if err != nil {
			return nil, newErr(KindInvalidSpec, 0, err, "source.query cannot be rendered")
		}
		if query == nil {
			query = map[string]string{}
		}
		pager.applyTo(query)

		// On fusionne avec la query déjà présente dans l'URL au lieu de l'écraser :
		// beaucoup d'URL sur-mesure embarquent déjà un paramètre fixe (un id de
		// webhook, une version d'API).
		values := parsedURL.Query()
		for k, v := range query {
			values.Set(k, v)
		}
		parsedURL.RawQuery = values.Encode()
		target = parsedURL.String()
	}

	var bodyReader io.Reader
	if spec.Source.Body != nil {
		rendered, err := renderBody(spec.Source.Body, vars)
		if err != nil {
			return nil, newErr(KindInvalidSpec, 0, err, "source.body cannot be rendered")
		}
		encoded, err := json.Marshal(rendered)
		if err != nil {
			return nil, newErr(KindInvalidSpec, 0, err, "source.body cannot be serialized")
		}
		bodyReader = bytes.NewReader(encoded)
	}

	req, err := http.NewRequestWithContext(ctx, spec.Source.Method, target, bodyReader)
	if err != nil {
		return nil, newErr(KindInvalidSpec, 0, err, "cannot build the request")
	}

	headers, err := RenderMap(spec.Source.Headers, vars)
	if err != nil {
		return nil, newErr(KindInvalidSpec, 0, err, "source.headers cannot be rendered")
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	if req.Header.Get("Accept") == "" {
		if spec.Records.Format == FormatCSV {
			req.Header.Set("Accept", "text/csv, */*")
		} else {
			req.Header.Set("Accept", "application/json")
		}
	}
	if bodyReader != nil && req.Header.Get("Content-Type") == "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if req.Header.Get("User-Agent") == "" {
		req.Header.Set("User-Agent", "Quanti-Connector/1.0")
	}

	return req, nil
}

// #endregion

// #region renderBody
// renderBody applique le templating récursivement dans un corps JSON. Seules les
// STRINGS sont templatées : un nombre ou un booléen n'a pas de variable à substituer.
func renderBody(body any, vars Vars) (any, error) {
	switch t := body.(type) {
	case string:
		if !strings.Contains(t, "{{") {
			return t, nil
		}
		return Render(t, vars)

	case map[string]any:
		out := make(map[string]any, len(t))
		for k, v := range t {
			rendered, err := renderBody(v, vars)
			if err != nil {
				return nil, err
			}
			out[k] = rendered
		}
		return out, nil

	case []any:
		out := make([]any, len(t))
		for i, v := range t {
			rendered, err := renderBody(v, vars)
			if err != nil {
				return nil, err
			}
			out[i] = rendered
		}
		return out, nil

	default:
		return body, nil
	}
}

// #endregion

// #region FetchN
// FetchN collecte au plus n lignes et s'arrête. C'est le mode de `test-query` et
// `infer-schema` : on veut un échantillon représentatif, pas 400 pages.
func (e *Engine) FetchN(ctx context.Context, spec *Spec, vars Vars, n int) ([]map[string]any, Stats, error) {
	if n <= 0 {
		n = 10
	}
	rows := make([]map[string]any, 0, n)

	stats, err := e.Fetch(ctx, spec, vars, func(row map[string]any) error {
		rows = append(rows, row)
		if len(rows) >= n {
			return ErrStop
		}
		return nil
	})
	if err != nil {
		return nil, stats, err
	}
	return rows, stats, nil
}

// #endregion
