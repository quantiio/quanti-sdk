package setup

// #region SetupResponse
// SetupResponse is the standard JSON output for setup commands
type SetupResponse struct {
	Accounts  []SetupAccount   `json:"accounts"`
	Hierarchy []HierarchyLevel `json:"hierarchy,omitempty"`
	ParentID  string           `json:"parent_id,omitempty"`
	Error     *SetupError      `json:"error,omitempty"`
}

// #region SetupAccount
// SetupAccount represents an account returned by list-accounts or list-children
type SetupAccount struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	AccountID string `json:"account_id,omitempty"` // Parent account ID (for hierarchical connectors)
	Currency  string `json:"currency,omitempty"`
	Timezone  string `json:"timezone,omitempty"`
	Level     string `json:"level,omitempty"` // Hierarchy level (e.g., "mcc", "customer")
}

// #region HierarchyLevel
// HierarchyLevel describes a level in the account hierarchy
type HierarchyLevel struct {
	Level       string `json:"level"`
	Label       string `json:"label"`
	LabelPlural string `json:"label_plural"`
}

// #region SetupError
// SetupError represents an error in setup commands
type SetupError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// #region OAuthCallbackResponse
// OAuthCallbackResponse is the JSON output from oauth-callback command
type OAuthCallbackResponse struct {
	Credentials *OAuthCredentialsResult `json:"credentials,omitempty"`
	Email       string                  `json:"email,omitempty"`
	Error       *SetupError             `json:"error,omitempty"`
}

// #region OAuthCredentialsResult
// OAuthCredentialsResult contains the OAuth tokens returned by the proc
type OAuthCredentialsResult struct {
	AccessToken  string            `json:"access_token"`
	RefreshToken string            `json:"refresh_token,omitempty"`
	TokenType    string            `json:"token_type,omitempty"`
	ExpiresAt    string            `json:"expires_at,omitempty"`
	Extra        map[string]string `json:"extra,omitempty"`
}

// #region TestQueryResponse
// TestQueryResponse is the JSON output from test-query command
type TestQueryResponse struct {
	Valid   bool        `json:"valid"`
	Message string      `json:"message,omitempty"`
	Error   *SetupError `json:"error,omitempty"`
}

// #region InferSchemaResponse
// InferSchemaResponse is the JSON output from infer-schema command
type InferSchemaResponse struct {
	Schema *InferredSchema `json:"schema,omitempty"`
	Error  *SetupError     `json:"error,omitempty"`
}

// #region InferredSchema
// InferredSchema represents the inferred schema from a query result
type InferredSchema struct {
	OrderedFields []InferredField `json:"orderedFields"`
}

// #region InferredField
// InferredField represents a field inferred from the query result
type InferredField struct {
	FieldID          string                `json:"fieldId"`
	FieldSrc         string                `json:"fieldSrc"`
	FieldPath        string                `json:"fieldPath"`
	DatabaseMetaData *InferredDatabaseMeta `json:"databaseMetaData"`
}

// #region InferredDatabaseMeta
// InferredDatabaseMeta contains inferred database metadata for a field
type InferredDatabaseMeta struct {
	Name         string `json:"name"`
	Type         string `json:"type"` // STRING, INTEGER, FLOAT, DATE, BOOLEAN
	Description  string `json:"description,omitempty"`
	IsMetric     bool   `json:"isMetric"`
	IsQuantiDate bool   `json:"isQuantiDate"`
	QuantiID     bool   `json:"quantiId"`
	Managed      bool   `json:"managed"`
}

// #region ValidateResponse
// ValidateResponse is the response for the validate command
type ValidateResponse struct {
	Valid bool        `json:"valid"`
	Error *SetupError `json:"error,omitempty"`
}
