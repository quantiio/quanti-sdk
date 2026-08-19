package httpsource

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
)

// Spec est la description COMPLÈTE d'un appel HTTP, telle qu'écrite dans le bloc
// `request` d'un report du conf.yml. C'est le contrat central du connecteur
// api-rest-v2 : il remplace le code Go compilé d'un binaire dédié par de la donnée.
//
// Toute évolution ici est un changement de contrat sur des conf.yml en production :
// n'ajouter que des champs OPTIONNELS avec un défaut rétro-compatible, et ne jamais
// changer la sémantique d'un champ existant.
type Spec struct {
	Source     Source     `json:"source"`
	Auth       Auth       `json:"auth"`
	Pagination Pagination `json:"pagination"`
	Retry      Retry      `json:"retry"`
	Records    Records    `json:"records"`
}

// Source décrit la requête de base. Les valeurs sont des templates (cf template.go) :
// `{{date}}`, `{{credentials.apikey}}`, `{{adAccount.id}}`…
type Source struct {
	Method         string            `json:"method"`
	URL            string            `json:"url"`
	Query          map[string]string `json:"query"`
	Headers        map[string]string `json:"headers"`
	Body           any               `json:"body"`
	TimeoutSeconds int               `json:"timeoutSeconds"`
}

// Auth : comment authentifier l'appel.
//
// Les modes oauth2_* couvrent les échanges SANS redirection navigateur, les seuls
// faisables depuis un binaire non interactif. L'`authorization_code` (3-legs) est
// hors périmètre : il exige un client OAuth déclaré au niveau du connecteur, donc
// partagé par tous les comptes — incompatible avec "une API différente par client".
type Auth struct {
	Mode         string   `json:"mode"`
	Name         string   `json:"name"`
	Value        string   `json:"value"`
	Username     string   `json:"username"`
	Password     string   `json:"password"`
	TokenURL     string   `json:"tokenUrl"`
	ClientID     string   `json:"clientId"`
	ClientSecret string   `json:"clientSecret"`
	RefreshToken string   `json:"refreshToken"`
	Scopes       []string `json:"scopes"`
	Audience     string   `json:"audience"`
}

const (
	AuthNone                    = "none"
	AuthHeader                  = "header"
	AuthQuery                   = "query"
	AuthBasic                   = "basic"
	AuthBearer                  = "bearer"
	AuthOAuth2ClientCredentials = "oauth2_client_credentials"
	AuthOAuth2Refresh           = "oauth2_refresh"
)

// Pagination : comment enchaîner les pages.
//
// `MaxPages` n'est PAS un plafond métier mais un garde-fou : une API qui renvoie
// éternellement un curseur non vide ferait tourner le process jusqu'au timeout du
// worker. On préfère s'arrêter avec un warning explicite.
type Pagination struct {
	Type           string `json:"type"`
	MaxPages       int    `json:"maxPages"`
	Param          string `json:"param"`
	StartAt        int    `json:"startAt"`
	SizeParam      string `json:"sizeParam"`
	Size           int    `json:"size"`
	StopWhen       string `json:"stopWhen"`
	CursorPath     string `json:"cursorPath"`
	HasMorePath    string `json:"hasMorePath"`
	NextURLPath    string `json:"nextUrlPath"`
	TotalPagesPath string `json:"totalPagesPath"`
}

const (
	PageNone       = "none"
	PagePage       = "page"
	PageOffset     = "offset"
	PageCursor     = "cursor"
	PageLinkHeader = "link_header"
	PageNextURL    = "next_url"
)

// Retry : politique de reprise. C'est le bloc le plus important en pratique — les
// deux connecteurs v1 migrés ne paginent pas, mais l'un d'eux vit ou meurt sur sa
// gestion du 429.
//
// Volontairement PAR REQUÊTE et pas global : chaque API tierce a ses propres travers
// (Les Échos sous-estime son Retry-After et exige +600 s), et c'est précisément ce
// genre de spécificité qui justifiait un binaire dédié avant.
type Retry struct {
	MaxAttempts int      `json:"maxAttempts"`
	On429       *On429   `json:"on429"`
	On5xx       *Backoff `json:"on5xx"`
	OnNetwork   *Backoff `json:"onNetwork"`
}

// On429 : gestion du rate limit.
//
// ExtraWaitSeconds existe pour les API qui MENTENT sur leur `Retry-After` : en le
// respectant à la lettre on se refait jeter immédiatement. Ce n'est pas un réglage de
// confort, c'est un contournement de bug tiers — quand il est posé, dire pourquoi
// dans le conf.yml.
type On429 struct {
	RespectRetryAfter bool   `json:"respectRetryAfter"`
	RetryAfterPath    string `json:"retryAfterPath"`
	ExtraWaitSeconds  int    `json:"extraWaitSeconds"`
	MaxWaitSeconds    int    `json:"maxWaitSeconds"`
	BackoffSeconds    int    `json:"backoffSeconds"`
	JitterMs          int    `json:"jitterMs"`
}

// Backoff : attente fixe + jitter avant une nouvelle tentative.
type Backoff struct {
	BackoffSeconds int  `json:"backoffSeconds"`
	JitterMs       int  `json:"jitterMs"`
	Exponential    bool `json:"exponential"`
}

// Records : comment extraire les lignes de la réponse.
type Records struct {
	Format string `json:"format"`
	Path   string `json:"path"`

	// Explode : nom d'un champ TABLEAU de la ligne. Chaque élément produit une ligne,
	// et le champ est remplacé par l'élément en OBJET SINGULIER (pas un tableau, pas
	// d'index) — c'est ce qui fait produire `data.items.<champ>` au flatten de
	// processor-v2 et non `data.items.0.<champ>`.
	Explode string `json:"explode"`

	// EmitWhenExplodeEmpty : tableau vide ou absent ⇒ émettre la ligne parente telle
	// quelle (colonnes exploded à NULL). Défaut true : perdre des commandes sans
	// ligne de détail serait une perte de donnée silencieuse.
	EmitWhenExplodeEmpty *bool `json:"emitWhenExplodeEmpty"`

	// Inject : champs ajoutés à chaque ligne AVANT émission. Appliqué à l'intérieur de
	// la ligne (donc de `data`) : processor-v2 ne conserve que `data.*` au flatten.
	Inject map[string]string `json:"inject"`

	CSV *CSVOptions `json:"csv"`
}

const (
	FormatJSON = "json"
	FormatCSV  = "csv"
)

// CSVOptions : options de parsing CSV. HasHeader par défaut true (le cas normal d'un
// export) ; sans en-tête il faut fournir Columns, sinon les colonnes seraient
// anonymes et le schéma inexploitable.
type CSVOptions struct {
	Delimiter string   `json:"delimiter"`
	HasHeader *bool    `json:"hasHeader"`
	Columns   []string `json:"columns"`
}

// #region ParseSpec
// ParseSpec construit une Spec depuis la valeur brute du bloc `request` du conf.yml
// (un map[string]any après passage par le SDK). On repasse par JSON plutôt que de
// lire le map à la main : un seul jeu de tags fait foi, et on hérite gratuitement des
// conversions de types.
//
// ⚠️ Le YAML du conf.yml peut produire des map[interface{}]interface{} (yaml.v2) que
// encoding/json refuse. normalizeForJSON les convertit d'abord — sans ça, la spec
// échoue en production alors qu'elle passe en test (où elle arrive en JSON).
func ParseSpec(raw any) (*Spec, error) {
	if raw == nil {
		return nil, newErr(KindInvalidSpec, 0, nil, "no request spec: the report has no `request` block in conf.yml")
	}

	normalized, err := normalizeForJSON(raw)
	if err != nil {
		return nil, newErr(KindInvalidSpec, 0, err, "request spec cannot be normalized")
	}

	buf, err := json.Marshal(normalized)
	if err != nil {
		return nil, newErr(KindInvalidSpec, 0, err, "request spec cannot be serialized")
	}

	var spec Spec
	dec := json.NewDecoder(strings.NewReader(string(buf)))
	if err := dec.Decode(&spec); err != nil {
		return nil, newErr(KindInvalidSpec, 0, err, "request spec cannot be parsed")
	}

	spec.applyDefaults()
	if err := spec.Validate(); err != nil {
		return nil, err
	}
	return &spec, nil
}

// #endregion

// #region normalizeForJSON
// normalizeForJSON convertit récursivement les map[interface{}]interface{} de yaml.v2
// en map[string]any. Les clés non-string sont refusées explicitement : silencieusement
// stringifier une clé numérique produirait une spec subtilement fausse.
func normalizeForJSON(v any) (any, error) {
	switch t := v.(type) {
	case map[any]any:
		out := make(map[string]any, len(t))
		for k, val := range t {
			ks, ok := k.(string)
			if !ok {
				return nil, fmt.Errorf("non-string key %v (%T) in the spec", k, k)
			}
			nv, err := normalizeForJSON(val)
			if err != nil {
				return nil, err
			}
			out[ks] = nv
		}
		return out, nil
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, val := range t {
			nv, err := normalizeForJSON(val)
			if err != nil {
				return nil, err
			}
			out[k] = nv
		}
		return out, nil
	case []any:
		out := make([]any, len(t))
		for i, val := range t {
			nv, err := normalizeForJSON(val)
			if err != nil {
				return nil, err
			}
			out[i] = nv
		}
		return out, nil
	default:
		return v, nil
	}
}

// #endregion

// #region applyDefaults
// applyDefaults pose les valeurs implicites. Objectif : qu'un conf.yml minimal
// (url + records.path) marche, pour que la spec la plus courante reste courte.
func (s *Spec) applyDefaults() {
	if s.Source.Method == "" {
		s.Source.Method = "GET"
	}
	s.Source.Method = strings.ToUpper(s.Source.Method)

	if s.Source.TimeoutSeconds <= 0 {
		s.Source.TimeoutSeconds = 60
	}

	if s.Auth.Mode == "" {
		s.Auth.Mode = AuthNone
	}

	if s.Pagination.Type == "" {
		s.Pagination.Type = PageNone
	}
	if s.Pagination.MaxPages <= 0 {
		s.Pagination.MaxPages = 1000
	}
	if s.Pagination.Type == PagePage && s.Pagination.Param == "" {
		s.Pagination.Param = "page"
	}
	if s.Pagination.Type == PageOffset && s.Pagination.Param == "" {
		s.Pagination.Param = "offset"
	}
	if s.Pagination.Type == PageCursor && s.Pagination.Param == "" {
		s.Pagination.Param = "cursor"
	}
	if s.Pagination.Type == PagePage && s.Pagination.StartAt == 0 {
		// La grande majorité des API pagine à partir de 1. Un conf.yml qui veut
		// vraiment démarrer à 0 doit l'écrire explicitement (startAt: 0 est
		// indistinguable de l'absence de clé en JSON, d'où ce défaut assumé).
		s.Pagination.StartAt = 1
	}

	if s.Retry.MaxAttempts <= 0 {
		s.Retry.MaxAttempts = 5
	}
	if s.Retry.On429 == nil {
		// Défaut sain : respecter le Retry-After. Une API sans 429 ne le déclenchera
		// jamais, donc aucun coût à l'activer par défaut.
		s.Retry.On429 = &On429{RespectRetryAfter: true, BackoffSeconds: 60, MaxWaitSeconds: 900}
	}
	if s.Retry.On429.BackoffSeconds <= 0 {
		s.Retry.On429.BackoffSeconds = 60
	}
	if s.Retry.On429.MaxWaitSeconds <= 0 {
		s.Retry.On429.MaxWaitSeconds = 900
	}
	if s.Retry.On5xx == nil {
		s.Retry.On5xx = &Backoff{BackoffSeconds: 5, JitterMs: 500, Exponential: true}
	}
	if s.Retry.OnNetwork == nil {
		s.Retry.OnNetwork = &Backoff{BackoffSeconds: 2, JitterMs: 500, Exponential: true}
	}

	if s.Records.Format == "" {
		s.Records.Format = FormatJSON
	}
	s.Records.Format = strings.ToLower(s.Records.Format)

	if s.Records.EmitWhenExplodeEmpty == nil {
		t := true
		s.Records.EmitWhenExplodeEmpty = &t
	}
	if s.Records.Format == FormatCSV {
		if s.Records.CSV == nil {
			s.Records.CSV = &CSVOptions{}
		}
		if s.Records.CSV.HasHeader == nil {
			t := true
			s.Records.CSV.HasHeader = &t
		}
		if s.Records.CSV.Delimiter == "" {
			s.Records.CSV.Delimiter = ","
		}
	}
}

// #endregion

// #region Validate
// Validate refuse une spec incohérente AVANT le premier appel réseau. C'est ce que
// `setup validate` expose à l'admin au moment du Save : mieux vaut un refus immédiat
// qu'une table vide découverte au cron de 3 h.
func (s *Spec) Validate() error {
	if strings.TrimSpace(s.Source.URL) == "" {
		return newErr(KindInvalidSpec, 0, nil, "source.url is required")
	}

	// L'URL contient des templates non encore résolus, on ne peut donc pas la parser
	// entièrement ici. On vérifie au moins le schéma : une URL sans https:// est
	// presque toujours un oubli de copier-coller.
	if !strings.HasPrefix(s.Source.URL, "http://") && !strings.HasPrefix(s.Source.URL, "https://") {
		return newErr(KindInvalidSpec, 0, nil, "source.url must start with http:// or https:// (got %q)", truncate(s.Source.URL, 60))
	}

	switch s.Source.Method {
	case "GET", "POST", "PUT", "PATCH", "DELETE":
	default:
		return newErr(KindInvalidSpec, 0, nil, "source.method %q is not supported", s.Source.Method)
	}

	if err := s.validateAuth(); err != nil {
		return err
	}
	if err := s.validatePagination(); err != nil {
		return err
	}

	switch s.Records.Format {
	case FormatJSON, FormatCSV:
	default:
		return newErr(KindInvalidSpec, 0, nil, "records.format %q is not supported (json or csv)", s.Records.Format)
	}

	if s.Records.Format == FormatCSV && !*s.Records.CSV.HasHeader && len(s.Records.CSV.Columns) == 0 {
		return newErr(KindInvalidSpec, 0, nil, "records.csv: columns is required when hasHeader is false, otherwise columns would be unnamed")
	}

	return nil
}

// #endregion

// #region validateAuth
func (s *Spec) validateAuth() error {
	switch s.Auth.Mode {
	case AuthNone:
		return nil

	case AuthHeader, AuthQuery:
		if s.Auth.Name == "" {
			return newErr(KindInvalidSpec, 0, nil, "auth.name is required with mode %q (header or query parameter name)", s.Auth.Mode)
		}
		if s.Auth.Value == "" {
			return newErr(KindInvalidSpec, 0, nil, "auth.value is required with mode %q", s.Auth.Mode)
		}
		return nil

	case AuthBearer:
		if s.Auth.Value == "" {
			return newErr(KindInvalidSpec, 0, nil, "auth.value is required with mode bearer")
		}
		return nil

	case AuthBasic:
		if s.Auth.Username == "" || s.Auth.Password == "" {
			return newErr(KindInvalidSpec, 0, nil, "auth.username and auth.password are required with mode basic")
		}
		return nil

	case AuthOAuth2ClientCredentials:
		if s.Auth.TokenURL == "" || s.Auth.ClientID == "" || s.Auth.ClientSecret == "" {
			return newErr(KindInvalidSpec, 0, nil, "auth.tokenUrl, auth.clientId and auth.clientSecret are required with mode %s", AuthOAuth2ClientCredentials)
		}
		return nil

	case AuthOAuth2Refresh:
		if s.Auth.TokenURL == "" || s.Auth.RefreshToken == "" {
			return newErr(KindInvalidSpec, 0, nil, "auth.tokenUrl and auth.refreshToken are required with mode %s", AuthOAuth2Refresh)
		}
		return nil

	default:
		return newErr(KindInvalidSpec, 0, nil,
			"auth.mode %q is not supported (none, header, query, basic, bearer, %s, %s)",
			s.Auth.Mode, AuthOAuth2ClientCredentials, AuthOAuth2Refresh)
	}
}

// #endregion

// #region validatePagination
func (s *Spec) validatePagination() error {
	switch s.Pagination.Type {
	case PageNone:
		return nil

	case PagePage:
		if s.Pagination.StopWhen == "totalPages" && s.Pagination.TotalPagesPath == "" {
			return newErr(KindInvalidSpec, 0, nil, "pagination.totalPagesPath is required when stopWhen is totalPages")
		}
		return nil

	case PageOffset:
		if s.Pagination.Size <= 0 {
			// Sans taille de page connue, on ne peut pas calculer l'offset suivant :
			// l'incrément serait une supposition.
			return newErr(KindInvalidSpec, 0, nil, "pagination.size is required with type offset (needed to compute the next offset)")
		}
		return nil

	case PageCursor:
		if s.Pagination.CursorPath == "" {
			return newErr(KindInvalidSpec, 0, nil, "pagination.cursorPath is required with type cursor")
		}
		return nil

	case PageNextURL:
		if s.Pagination.NextURLPath == "" {
			return newErr(KindInvalidSpec, 0, nil, "pagination.nextUrlPath is required with type next_url")
		}
		return nil

	case PageLinkHeader:
		return nil

	default:
		return newErr(KindInvalidSpec, 0, nil,
			"pagination.type %q is not supported (none, page, offset, cursor, link_header, next_url)", s.Pagination.Type)
	}
}

// #endregion

// #region SecretTemplates
// SecretTemplates retourne toutes les valeurs de la spec qui contiennent une
// référence à une credential. Le moteur s'en sert pour construire le rédacteur : tout
// ce qui sort de là ne doit JAMAIS apparaître en clair dans un log ou un message
// d'erreur — une clé en query param finirait sinon dans Loki et dans qm_process.
func (s *Spec) SecretTemplates() []string {
	var out []string
	add := func(v string) {
		if strings.Contains(v, credentialsPrefix) {
			out = append(out, v)
		}
	}
	add(s.Source.URL)
	for _, v := range s.Source.Query {
		add(v)
	}
	for _, v := range s.Source.Headers {
		add(v)
	}
	add(s.Auth.Value)
	add(s.Auth.Username)
	add(s.Auth.Password)
	add(s.Auth.ClientID)
	add(s.Auth.ClientSecret)
	add(s.Auth.RefreshToken)
	return out
}

// #endregion

// #region truncate
func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}

// #endregion

// #region redactURL
// redactURL retire la query string d'une URL pour les logs. Complément du rédacteur
// par valeur : couvre le cas d'un token qui aurait fuité dans l'URL par un chemin
// qu'on n'a pas anticipé (redirection, next_url renvoyé par l'API…).
func redactURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return "<unparsable url>"
	}
	if u.RawQuery == "" {
		return u.Scheme + "://" + u.Host + u.Path
	}
	return u.Scheme + "://" + u.Host + u.Path + "?<redacted>"
}

// #endregion
