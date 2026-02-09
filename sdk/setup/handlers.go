package setup

// #region SetupHandler
// SetupHandler is the base interface that each connector implements
type SetupHandler interface {
	// Commands returns the custom subcommands supported by this connector
	// Standard commands (list-accounts, list-children, etc.) are handled via optional interfaces
	Commands() []CommandDef
}

// #region CommandDef
// CommandDef describes a custom subcommand
type CommandDef struct {
	Name        string                        // Command name (e.g., "list-mcc-hierarchy")
	Description string                        // Short description for help
	Flags       []FlagDef                     // Expected flags
	Handler     func(ctx *CommandContext) error // Handler function
}

// #region FlagDef
// FlagDef describes a command flag
type FlagDef struct {
	Name        string // Flag name without -- prefix (e.g., "parent-id")
	Short       string // Short flag (e.g., "p")
	Description string // Description for help
	Required    bool   // Whether the flag is required
	Default     string // Default value
}

// #region CommandContext
// CommandContext provides context for command handlers
type CommandContext struct {
	Config *SetupConfig      // Loaded configuration
	Args   []string          // Remaining arguments after flag parsing
	Flags  map[string]string // Parsed flags
}

// #region Flag
// Flag returns the value of a flag, or empty string if not set
func (ctx *CommandContext) Flag(name string) string {
	if ctx.Flags == nil {
		return ""
	}
	return ctx.Flags[name]
}

// #region FlagOrDefault
// FlagOrDefault returns the value of a flag, or the default if not set
func (ctx *CommandContext) FlagOrDefault(name, defaultValue string) string {
	if val := ctx.Flag(name); val != "" {
		return val
	}
	return defaultValue
}

// #region HasFlag
// HasFlag checks if a flag was provided
func (ctx *CommandContext) HasFlag(name string) bool {
	_, ok := ctx.Flags[name]
	return ok
}

// ============================================================================
// Standard interfaces - Implement only what your connector needs
// ============================================================================

// #region AccountLister
// AccountLister interface for connectors that support listing accounts
type AccountLister interface {
	ListAccounts(ctx *CommandContext) (*SetupResponse, error)
}

// #region ChildrenLister
// ChildrenLister interface for connectors with hierarchical accounts
type ChildrenLister interface {
	ListChildren(ctx *CommandContext, parentID, level string) (*SetupResponse, error)
}

// #region OAuthHandler
// OAuthHandler interface for connectors that support OAuth token exchange
type OAuthHandler interface {
	OAuthCallback(ctx *CommandContext) (*OAuthCallbackResponse, error)
}

// #region QueryTester
// QueryTester interface for connectors that support custom query testing
type QueryTester interface {
	TestQuery(ctx *CommandContext) (*TestQueryResponse, error)
}

// #region SchemaInferrer
// SchemaInferrer interface for connectors that support schema inference
type SchemaInferrer interface {
	InferSchema(ctx *CommandContext) (*InferSchemaResponse, error)
}

// #region Validator
// Validator interface for connectors that support credential validation
type Validator interface {
	Validate(ctx *CommandContext) (*SetupResponse, error)
}
