package setup

import (
	"encoding/json"
	"fmt"
	"os"
)

// #region SetupConfig
// SetupConfig represents the temporary configuration file structure
// This mirrors the session.TempConfig from connectors-api
type SetupConfig struct {
	SKU string `json:"sku"`

	// PersonalCredentials contains OAuth tokens
	PersonalCredentials PersonalCredentials `json:"personalCredentials"`

	// ConnectorCredentials contains connector-specific params (supports both snake_case and camelCase)
	ConnectorCredentials map[string]any `json:"connectorCredentials"`

	// ConnectorConf contains business configuration
	ConnectorConf *ConnectorConf `json:"connectorConf,omitempty"`

	// OAuthCallback contains temporary OAuth callback params for token exchange
	OAuthCallback *OAuthCallbackParams `json:"oauthCallback,omitempty"`

	// TestRequest contains the custom request being tested (for test-query and infer-schema commands)
	TestRequest *TestRequestParams `json:"testRequest,omitempty"`
}

// #region PersonalCredentials
// PersonalCredentials contains OAuth tokens
type PersonalCredentials struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ClientID     string `json:"client_id"`
	ClientSecret string `json:"client_secret"`
	ExpiresIn    int    `json:"expires_in,omitempty"`
}

// #region ConnectorConf
// ConnectorConf contains business configuration
type ConnectorConf struct {
	AdAccounts []AdAccountConf `json:"adaccounts,omitempty"`
	Requests   []RequestConf   `json:"requests,omitempty"`
}

// #region AdAccountConf
// AdAccountConf represents an ad account in the configuration
type AdAccountConf struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	AccountID string `json:"account_id,omitempty"`
}

// #region RequestConf
// RequestConf represents a request in the configuration
type RequestConf struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Type string `json:"type"`
}

// #region OAuthCallbackParams
// OAuthCallbackParams contains the OAuth callback parameters needed for token exchange
type OAuthCallbackParams struct {
	Code         string            `json:"code"`
	RedirectURI  string            `json:"redirect_uri"`
	CodeVerifier string            `json:"code_verifier,omitempty"`
	Extra        map[string]string `json:"extra,omitempty"`
}

// #region TestRequestParams
// TestRequestParams contains the custom request parameters for testing.
//
// Les 4 champs nommés couvrent le modèle "rapport analytics" (report + fields +
// filters + sorts) partagé par la majorité des connecteurs. Ils restent la voie
// normale : ne pas passer par Raw quand ils suffisent.
//
// Raw conserve l'objet `testRequest` COMPLET tel que l'API l'a écrit, clés inconnues
// de cette struct incluses. C'est indispensable aux connecteurs dont la requête est
// une structure arbitraire décrite dans le conf.yml (api-rest-v2 :
// source/auth/pagination/retry/records) : sans ça, test-query et infer-schema ne
// peuvent pas recevoir la spec à tester, et l'aller-retour "j'écris ma requête → je
// la teste → j'en déduis le schéma" est impossible.
//
// Utiliser Decode (struct typée) ou AsMap (moteur générique) pour lire Raw.
type TestRequestParams struct {
	Report  string   `json:"report"`
	Fields  []string `json:"fields,omitempty"`
	Filters []string `json:"filters,omitempty"`
	Sorts   []string `json:"sorts,omitempty"`

	// Raw n'est pas peuplé par le décodage de struct classique (tag "-") mais par
	// UnmarshalJSON ci-dessous, qui conserve les octets d'origine.
	Raw json.RawMessage `json:"-"`
}

// #region TestRequestParams.UnmarshalJSON
// UnmarshalJSON remplit les champs nommés ET conserve les octets bruts.
//
// L'alias local est obligatoire : décoder dans TestRequestParams rappellerait cette
// méthode et bouclerait à l'infini.
func (p *TestRequestParams) UnmarshalJSON(data []byte) error {
	type alias TestRequestParams
	var a alias
	if err := json.Unmarshal(data, &a); err != nil {
		return err
	}
	*p = TestRequestParams(a)
	p.Raw = append(json.RawMessage(nil), data...)
	return nil
}

// #region TestRequestParams.Decode
// Decode désérialise la requête brute dans v (typiquement la struct de spec du
// connecteur). Erreur explicite si aucune requête n'a été transmise : un "spec vide"
// silencieux se traduirait par un test réussi… sur rien.
func (p *TestRequestParams) Decode(v any) error {
	if p == nil || len(p.Raw) == 0 {
		return fmt.Errorf("no testRequest provided in the setup config")
	}
	return json.Unmarshal(p.Raw, v)
}

// #region TestRequestParams.AsMap
// AsMap retourne la requête brute en map, pour les moteurs génériques qui parsent
// eux-mêmes leur spec depuis un map[string]any — même chemin de parsing en test
// qu'en production.
func (p *TestRequestParams) AsMap() (map[string]any, error) {
	var m map[string]any
	if err := p.Decode(&m); err != nil {
		return nil, err
	}
	return m, nil
}

// #region LoadSetupConfig
// LoadSetupConfig loads configuration from a file path
func LoadSetupConfig(path string) (*SetupConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}

	var config SetupConfig
	if err := json.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("failed to parse config file: %w", err)
	}

	return &config, nil
}

// #region GetCredentialString
// GetCredentialString retrieves a string value from connector credentials with fallback keys
// Supports both snake_case and camelCase variants
func GetCredentialString(creds map[string]any, keys ...string) string {
	for _, key := range keys {
		if val, ok := creds[key]; ok {
			if str, ok := val.(string); ok && str != "" {
				return str
			}
		}
	}
	return ""
}

// #region GetClientID
// GetClientID returns the OAuth client ID from connector or personal credentials
func (c *SetupConfig) GetClientID() string {
	clientID := GetCredentialString(c.ConnectorCredentials, "client_id", "clientId")
	if clientID != "" {
		return clientID
	}
	return c.PersonalCredentials.ClientID
}

// #region GetClientSecret
// GetClientSecret returns the OAuth client secret from connector or personal credentials
func (c *SetupConfig) GetClientSecret() string {
	clientSecret := GetCredentialString(c.ConnectorCredentials, "client_secret", "clientSecret")
	if clientSecret != "" {
		return clientSecret
	}
	return c.PersonalCredentials.ClientSecret
}

// #region GetAccessToken
// GetAccessToken returns the access token from personal credentials
func (c *SetupConfig) GetAccessToken() string {
	return c.PersonalCredentials.AccessToken
}

// #region GetRefreshToken
// GetRefreshToken returns the refresh token from personal credentials
func (c *SetupConfig) GetRefreshToken() string {
	return c.PersonalCredentials.RefreshToken
}

// #region HasOAuthTokens
// HasOAuthTokens checks if OAuth tokens are available
func (c *SetupConfig) HasOAuthTokens() bool {
	return c.PersonalCredentials.AccessToken != "" || c.PersonalCredentials.RefreshToken != ""
}
