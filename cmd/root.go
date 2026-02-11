package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/timvw/pane-patrol/internal/evaluator"
	"github.com/timvw/pane-patrol/internal/mux"
)

var (
	// Global flags.
	flagMux       string
	flagProvider  string
	flagModel     string
	flagBaseURL   string
	flagAPIKey    string
	flagMaxTokens int64
	flagVerbose   bool
)

var rootCmd = &cobra.Command{
	Use:   "pane-patrol",
	Short: "ZFC-compliant terminal pane monitor for AI coding agents",
	Long: `pane-patrol monitors terminal multiplexer panes for blocked AI coding agents.

It uses an LLM to evaluate pane content and determine whether an AI agent
is waiting for human input (confirmation dialogs, permission prompts, etc.).

Following ZFC (Zero False Commands) principles, all judgment calls are made
by the LLM — Go code only provides transport.`,
}

// Execute runs the root command.
func Execute() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

func init() {
	rootCmd.PersistentFlags().StringVar(&flagMux, "mux", envOrDefault("PANE_PATROL_MUX", ""), "terminal multiplexer: tmux, zellij (default: auto-detect)")
	rootCmd.PersistentFlags().StringVar(&flagProvider, "provider", envOrDefault("PANE_PATROL_PROVIDER", "anthropic"), "LLM provider: anthropic, openai")
	rootCmd.PersistentFlags().StringVar(&flagModel, "model", envOrDefault("PANE_PATROL_MODEL", ""), "LLM model name (default: claude-sonnet-4-5 for anthropic, gpt-4o-mini for openai)")
	rootCmd.PersistentFlags().StringVar(&flagBaseURL, "base-url", envOrDefault("PANE_PATROL_BASE_URL", ""), "override LLM API base URL")
	rootCmd.PersistentFlags().StringVar(&flagAPIKey, "api-key", envOrDefault("PANE_PATROL_API_KEY", ""), "override LLM API key")
	rootCmd.PersistentFlags().Int64Var(&flagMaxTokens, "max-tokens", 0, "max completion tokens (default: 4096; increase for reasoning models)")
	rootCmd.PersistentFlags().BoolVar(&flagVerbose, "verbose", false, "include raw pane content in output")
}

// getMultiplexer returns the configured or auto-detected multiplexer.
func getMultiplexer() (mux.Multiplexer, error) {
	if flagMux != "" {
		return mux.FromName(flagMux)
	}
	return mux.Detect()
}

// getEvaluator returns the configured LLM evaluator.
func getEvaluator() (evaluator.Evaluator, error) {
	switch flagProvider {
	case "anthropic":
		return newAnthropicEvaluator()
	case "openai":
		return newOpenAIEvaluator()
	default:
		return nil, fmt.Errorf("unknown provider %q (supported: anthropic, openai)", flagProvider)
	}
}

// newAnthropicEvaluator creates an Anthropic evaluator with the resolved config.
func newAnthropicEvaluator() (evaluator.Evaluator, error) {
	model := flagModel
	if model == "" {
		model = "claude-sonnet-4-5"
	}

	baseURL := flagBaseURL
	apiKey := flagAPIKey
	extraHeaders := map[string]string{}

	// Resolve base URL and API key from environment.
	if baseURL == "" {
		resourceName := os.Getenv("AZURE_RESOURCE_NAME")
		if resourceName != "" {
			// The Anthropic SDK appends /v1/messages to the base URL.
			// Azure AI Foundry endpoint is: https://<resource>.services.ai.azure.com/anthropic/v1/messages
			// So we set base URL to .../anthropic/ (SDK adds v1/messages).
			baseURL = fmt.Sprintf("https://%s.services.ai.azure.com/anthropic/", resourceName)
		}
	}
	if apiKey == "" {
		apiKey = os.Getenv("AZURE_OPENAI_API_KEY")
	}
	if apiKey == "" {
		apiKey = os.Getenv("ANTHROPIC_API_KEY")
		// Direct Anthropic API: SDK default base URL is https://api.anthropic.com/
		// No need to override — just let the SDK use its default.
	}

	if apiKey == "" {
		return nil, fmt.Errorf("no API key found. Set PANE_PATROL_API_KEY, AZURE_OPENAI_API_KEY, or ANTHROPIC_API_KEY")
	}

	// Azure AI Foundry needs both "api-key" (Azure) and "x-api-key" (Anthropic SDK default) headers.
	if os.Getenv("AZURE_RESOURCE_NAME") != "" || isAzureEndpoint(baseURL) {
		extraHeaders["api-key"] = apiKey
	}

	return evaluator.NewAnthropicEvaluator(evaluator.AnthropicConfig{
		BaseURL:      baseURL,
		APIKey:       apiKey,
		Model:        model,
		MaxTokens:    flagMaxTokens,
		ExtraHeaders: extraHeaders,
	}), nil
}

// newOpenAIEvaluator creates an OpenAI evaluator with the resolved config.
func newOpenAIEvaluator() (evaluator.Evaluator, error) {
	model := flagModel
	if model == "" {
		model = "gpt-4o-mini"
	}

	baseURL := flagBaseURL
	apiKey := flagAPIKey
	extraHeaders := map[string]string{}

	if baseURL == "" {
		resourceName := os.Getenv("AZURE_RESOURCE_NAME")
		if resourceName != "" {
			baseURL = fmt.Sprintf("https://%s.openai.azure.com/openai/v1", resourceName)
		}
	}
	if apiKey == "" {
		apiKey = os.Getenv("AZURE_OPENAI_API_KEY")
	}
	if apiKey == "" {
		apiKey = os.Getenv("OPENAI_API_KEY")
	}

	if apiKey == "" {
		return nil, fmt.Errorf("no API key found. Set PANE_PATROL_API_KEY, AZURE_OPENAI_API_KEY, or OPENAI_API_KEY")
	}

	if os.Getenv("AZURE_RESOURCE_NAME") != "" || isAzureEndpoint(baseURL) {
		extraHeaders["api-key"] = apiKey
	}

	return evaluator.NewOpenAIEvaluator(evaluator.OpenAIConfig{
		BaseURL:      baseURL,
		APIKey:       apiKey,
		Model:        model,
		MaxTokens:    flagMaxTokens,
		ExtraHeaders: extraHeaders,
	}), nil
}

// isAzureEndpoint checks if a URL is an Azure endpoint.
func isAzureEndpoint(url string) bool {
	return len(url) > 0 && (contains(url, ".azure.com") || contains(url, ".azure.us"))
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && searchString(s, substr)
}

func searchString(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

func envOrDefault(key, defaultValue string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return defaultValue
}
