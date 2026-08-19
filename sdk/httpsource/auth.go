package httpsource

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// authenticator porte l'état d'authentification d'une collecte : pour les modes
// oauth2_*, le token obtenu est réutilisé sur toutes les pages plutôt que redemandé à
// chaque requête (certains providers rate-limitent durement l'endpoint token).
type authenticator struct {
	spec     *Spec
	vars     Vars
	client   *http.Client
	redactor *Redactor

	token       string
	tokenExpiry time.Time
}

// #region newAuthenticator
func newAuthenticator(spec *Spec, vars Vars, client *http.Client, redactor *Redactor) *authenticator {
	return &authenticator{spec: spec, vars: vars, client: client, redactor: redactor}
}

// #endregion

// #region apply
// apply pose l'authentification sur la requête. Appelé pour CHAQUE requête (chaque
// page), donc doit rester idempotent et peu coûteux.
func (a *authenticator) apply(ctx context.Context, req *http.Request) error {
	auth := a.spec.Auth

	switch auth.Mode {
	case AuthNone:
		return nil

	case AuthHeader:
		value, err := Render(auth.Value, a.vars)
		if err != nil {
			return err
		}
		req.Header.Set(auth.Name, value)
		return nil

	case AuthQuery:
		value, err := Render(auth.Value, a.vars)
		if err != nil {
			return err
		}
		q := req.URL.Query()
		q.Set(auth.Name, value)
		req.URL.RawQuery = q.Encode()
		return nil

	case AuthBearer:
		value, err := Render(auth.Value, a.vars)
		if err != nil {
			return err
		}
		req.Header.Set("Authorization", "Bearer "+value)
		return nil

	case AuthBasic:
		user, err := Render(auth.Username, a.vars)
		if err != nil {
			return err
		}
		pass, err := Render(auth.Password, a.vars)
		if err != nil {
			return err
		}
		// SetBasicAuth ferait la même chose, mais on encode nous-mêmes pour pouvoir
		// enregistrer la valeur encodée dans le rédacteur : sans ça, le couple
		// user:pass en base64 pourrait fuiter dans un dump de headers.
		encoded := base64.StdEncoding.EncodeToString([]byte(user + ":" + pass))
		a.redactor.Add(encoded)
		req.Header.Set("Authorization", "Basic "+encoded)
		return nil

	case AuthOAuth2ClientCredentials, AuthOAuth2Refresh:
		token, err := a.ensureToken(ctx)
		if err != nil {
			return err
		}
		req.Header.Set("Authorization", "Bearer "+token)
		return nil

	default:
		// Inatteignable : Validate() a déjà rejeté les modes inconnus. On le traite
		// quand même pour ne pas authentifier silencieusement "en clair" si un mode
		// est ajouté à Validate sans l'être ici.
		return newErr(KindInvalidSpec, 0, nil, "auth.mode %q is not implemented", auth.Mode)
	}
}

// #endregion

// #region ensureToken
// ensureToken renvoie un access_token valide, en le renouvelant si besoin.
//
// La marge de 30 s évite le cas fâcheux d'un token qui expire ENTRE la vérification et
// l'arrivée de la requête chez le provider (un 401 en milieu de pagination, donc une
// collecte partielle).
func (a *authenticator) ensureToken(ctx context.Context) (string, error) {
	if a.token != "" && time.Now().Add(30*time.Second).Before(a.tokenExpiry) {
		return a.token, nil
	}
	return a.fetchToken(ctx)
}

// #endregion

// #region invalidateToken
// invalidateToken force le renouvellement au prochain appel. Utilisé sur 401 : un
// token peut avoir été révoqué avant son expiration annoncée.
func (a *authenticator) invalidateToken() {
	a.token = ""
	a.tokenExpiry = time.Time{}
}

// #endregion

// #region usesToken
func (a *authenticator) usesToken() bool {
	return a.spec.Auth.Mode == AuthOAuth2ClientCredentials || a.spec.Auth.Mode == AuthOAuth2Refresh
}

// #endregion

// #region fetchToken
func (a *authenticator) fetchToken(ctx context.Context) (string, error) {
	auth := a.spec.Auth

	tokenURL, err := Render(auth.TokenURL, a.vars)
	if err != nil {
		return "", err
	}
	clientID, err := Render(auth.ClientID, a.vars)
	if err != nil {
		return "", err
	}
	clientSecret, err := Render(auth.ClientSecret, a.vars)
	if err != nil {
		return "", err
	}

	form := url.Values{}
	if auth.Mode == AuthOAuth2ClientCredentials {
		form.Set("grant_type", "client_credentials")
	} else {
		refreshToken, rErr := Render(auth.RefreshToken, a.vars)
		if rErr != nil {
			return "", rErr
		}
		form.Set("grant_type", "refresh_token")
		form.Set("refresh_token", refreshToken)
	}
	if clientID != "" {
		form.Set("client_id", clientID)
	}
	if clientSecret != "" {
		form.Set("client_secret", clientSecret)
	}
	if len(auth.Scopes) > 0 {
		form.Set("scope", strings.Join(auth.Scopes, " "))
	}
	if auth.Audience != "" {
		form.Set("audience", auth.Audience)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", newErr(KindInvalidSpec, 0, err, "cannot build the token request")
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	resp, err := a.client.Do(req)
	if err != nil {
		return "", newErr(KindUnavailable, 0, err, "token endpoint unreachable (%s)", redactURL(tokenURL))
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", newErr(KindUnavailable, resp.StatusCode, err, "cannot read the token response")
	}

	if resp.StatusCode != http.StatusOK {
		// Un token refusé est une erreur d'AUTH, pas d'indisponibilité : la retenter
		// ne servirait à rien, il faut de nouvelles credentials.
		return "", newErr(KindAuth, resp.StatusCode, nil,
			"token endpoint refused the credentials: %s", truncate(string(body), 200))
	}

	var parsed struct {
		AccessToken string `json:"access_token"`
		TokenType   string `json:"token_type"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return "", newErr(KindAuth, resp.StatusCode, err, "token response is not valid JSON")
	}
	if parsed.AccessToken == "" {
		return "", newErr(KindAuth, resp.StatusCode, nil, "token response contains no access_token")
	}

	// Le token n'était pas dans les credentials du compte : sans cet Add, il
	// apparaîtrait en clair dans le message d'erreur de la requête suivante.
	a.redactor.Add(parsed.AccessToken)

	a.token = parsed.AccessToken
	// Pas d'expires_in ⇒ on suppose une heure. Assez court pour ne pas s'accrocher à
	// un token mort, assez long pour ne pas marteler l'endpoint token à chaque page.
	ttl := time.Duration(parsed.ExpiresIn) * time.Second
	if parsed.ExpiresIn <= 0 {
		ttl = time.Hour
	}
	a.tokenExpiry = time.Now().Add(ttl)

	return a.token, nil
}

// #endregion

// #region describeAuth
// describeAuth donne une description NON sensible du mode d'auth, pour les logs.
func describeAuth(spec *Spec) string {
	switch spec.Auth.Mode {
	case AuthHeader, AuthQuery:
		return fmt.Sprintf("%s(%s)", spec.Auth.Mode, spec.Auth.Name)
	default:
		return spec.Auth.Mode
	}
}

// #endregion
