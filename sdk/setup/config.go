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
	Code         string `json:"code"`
	RedirectURI  string `json:"redirect_uri"`
	CodeVerifier string `json:"code_verifier,omitempty"`
}

// #region TestRequestParams
// TestRequestParams contains the custom request parameters for testing
type TestRequestParams struct {
	Report  string   `json:"report"`
	Fields  []string `json:"fields,omitempty"`
	Filters []string `json:"filters,omitempty"`
	Sorts   []string `json:"sorts,omitempty"`
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
