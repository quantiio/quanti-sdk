package setup

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// #region Run
// Run is the main entry point for setup commands
// Usage: <binary> setup <subcommand> [--config=<path>] [flags]
func Run(handler SetupHandler, args []string) {
	if len(args) < 1 {
		OutputError("USAGE_ERROR", buildUsage(handler))
		return
	}

	subcommand := args[0]

	// Parse global flags (--config)
	configPath, flags, remainingArgs := parseArgs(args[1:])

	// Build command context
	ctx := &CommandContext{
		Args:  remainingArgs,
		Flags: flags,
	}

	// Load config if path provided
	if configPath != "" {
		config, err := LoadSetupConfig(configPath)
		if err != nil {
			OutputError("CONFIG_ERROR", fmt.Sprintf("Failed to load config file: %v", err))
			return
		}
		ctx.Config = config
	}

	// Try custom commands first
	for _, cmd := range handler.Commands() {
		if cmd.Name == subcommand {
			if err := cmd.Handler(ctx); err != nil {
				OutputError("EXECUTION_ERROR", err.Error())
			}
			return
		}
	}

	// Try standard commands via interfaces
	if err := runStandardCommand(handler, subcommand, ctx); err != nil {
		OutputError("EXECUTION_ERROR", err.Error())
	}
}

// #region runStandardCommand
// runStandardCommand dispatches to standard interface methods
func runStandardCommand(handler SetupHandler, cmd string, ctx *CommandContext) error {
	switch cmd {
	case "list-accounts":
		if h, ok := handler.(AccountLister); ok {
			resp, err := h.ListAccounts(ctx)
			return outputResult(resp, err)
		}
		return fmt.Errorf("this connector does not support list-accounts")

	case "list-children":
		if h, ok := handler.(ChildrenLister); ok {
			parentID := ctx.Flag("parent-id")
			if parentID == "" {
				parentID = ctx.Flag("parent")
			}
			if parentID == "" {
				return fmt.Errorf("list-children requires --parent-id=<id> or --parent=<id>")
			}
			level := ctx.FlagOrDefault("level", "")
			resp, err := h.ListChildren(ctx, parentID, level)
			return outputResult(resp, err)
		}
		return fmt.Errorf("this connector does not support list-children")

	case "oauth-callback":
		if h, ok := handler.(OAuthHandler); ok {
			if ctx.Config == nil {
				OutputOAuthError("CONFIG_REQUIRED", "oauth-callback requires --config=<path>")
				return nil
			}
			resp, err := h.OAuthCallback(ctx)
			return outputOAuthResult(resp, err)
		}
		return fmt.Errorf("this connector does not support oauth-callback")

	case "test-query":
		if h, ok := handler.(QueryTester); ok {
			if ctx.Config == nil {
				return fmt.Errorf("test-query requires --config=<path>")
			}
			resp, err := h.TestQuery(ctx)
			return outputTestQueryResult(resp, err)
		}
		return fmt.Errorf("this connector does not support test-query")

	case "infer-schema":
		if h, ok := handler.(SchemaInferrer); ok {
			if ctx.Config == nil {
				return fmt.Errorf("infer-schema requires --config=<path>")
			}
			resp, err := h.InferSchema(ctx)
			return outputInferSchemaResult(resp, err)
		}
		return fmt.Errorf("this connector does not support infer-schema")

	case "validate":
		if h, ok := handler.(Validator); ok {
			resp, err := h.Validate(ctx)
			return outputResult(resp, err)
		}
		return fmt.Errorf("this connector does not support validate")

	default:
		return fmt.Errorf("unknown command: %s", cmd)
	}
}

// #region parseArgs
// parseArgs extracts --config and other flags from args
func parseArgs(args []string) (configPath string, flags map[string]string, remaining []string) {
	flags = make(map[string]string)

	for _, arg := range args {
		if strings.HasPrefix(arg, "--config=") {
			configPath = strings.TrimPrefix(arg, "--config=")
		} else if strings.HasPrefix(arg, "--") {
			// Parse --flag=value or --flag
			flagPart := strings.TrimPrefix(arg, "--")
			if idx := strings.Index(flagPart, "="); idx != -1 {
				flags[flagPart[:idx]] = flagPart[idx+1:]
			} else {
				flags[flagPart] = "true"
			}
		} else {
			remaining = append(remaining, arg)
		}
	}

	return
}

// #region buildUsage
// buildUsage generates usage text from handler commands
func buildUsage(handler SetupHandler) string {
	var sb strings.Builder
	sb.WriteString("Usage: setup <subcommand> [--config=<path>] [flags]\n\n")
	sb.WriteString("Standard commands:\n")
	sb.WriteString("  list-accounts     List accessible accounts\n")
	sb.WriteString("  list-children     List child accounts (--parent-id=<id>)\n")
	sb.WriteString("  oauth-callback    Exchange OAuth code for tokens (--config required)\n")
	sb.WriteString("  test-query        Test a custom query (--config required)\n")
	sb.WriteString("  infer-schema      Infer schema from fields (--config required)\n")
	sb.WriteString("  validate          Validate credentials\n")

	customCmds := handler.Commands()
	if len(customCmds) > 0 {
		sb.WriteString("\nConnector-specific commands:\n")
		for _, cmd := range customCmds {
			sb.WriteString(fmt.Sprintf("  %-17s %s\n", cmd.Name, cmd.Description))
		}
	}

	return sb.String()
}

// ============================================================================
// Output helpers
// ============================================================================

// #region OutputError
// OutputError outputs an error response and exits with code 1
func OutputError(code, message string) {
	response := SetupResponse{
		Error: &SetupError{
			Code:    code,
			Message: message,
		},
	}
	output, _ := json.Marshal(response)
	fmt.Println(string(output))
	os.Exit(1)
}

// #region OutputOAuthError
// OutputOAuthError outputs an OAuth error response and exits with code 1
func OutputOAuthError(code, message string) {
	response := OAuthCallbackResponse{
		Error: &SetupError{
			Code:    code,
			Message: message,
		},
	}
	output, _ := json.Marshal(response)
	fmt.Println(string(output))
	os.Exit(1)
}

// #region OutputJSON
// OutputJSON outputs any struct as JSON
func OutputJSON(v any) error {
	output, err := json.Marshal(v)
	if err != nil {
		return err
	}
	fmt.Println(string(output))
	return nil
}

// #region outputResult
// outputResult outputs a SetupResponse and handles errors
func outputResult(resp *SetupResponse, err error) error {
	if err != nil {
		return err
	}
	output, _ := json.Marshal(resp)
	fmt.Println(string(output))
	if resp.Error != nil {
		os.Exit(1)
	}
	return nil
}

// #region outputOAuthResult
// outputOAuthResult outputs an OAuthCallbackResponse and handles errors
func outputOAuthResult(resp *OAuthCallbackResponse, err error) error {
	if err != nil {
		return err
	}
	output, _ := json.Marshal(resp)
	fmt.Println(string(output))
	if resp.Error != nil {
		os.Exit(1)
	}
	return nil
}

// #region outputTestQueryResult
// outputTestQueryResult outputs a TestQueryResponse and handles errors
func outputTestQueryResult(resp *TestQueryResponse, err error) error {
	if err != nil {
		return err
	}
	output, _ := json.Marshal(resp)
	fmt.Println(string(output))
	if resp.Error != nil {
		os.Exit(1)
	}
	return nil
}

// #region outputInferSchemaResult
// outputInferSchemaResult outputs an InferSchemaResponse and handles errors
func outputInferSchemaResult(resp *InferSchemaResponse, err error) error {
	if err != nil {
		return err
	}
	output, _ := json.Marshal(resp)
	fmt.Println(string(output))
	if resp.Error != nil {
		os.Exit(1)
	}
	return nil
}
