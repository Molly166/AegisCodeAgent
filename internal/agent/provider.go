package agent

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
)

const DefaultOrcaRouterBaseURL = "https://api.orcarouter.ai/v1"

// ModelCapabilities is a wire-protocol contract for one selected model. It is
// deliberately not inferred from a vendor prefix or an automatic router name.
type ModelCapabilities struct {
	ToolCalling     bool   `json:"tool_calling"`
	JSONOutput      bool   `json:"json_output"`
	Reasoning       string `json:"reasoning"`
	ReplayReasoning bool   `json:"replay_reasoning"`
	TokenLimitField string `json:"token_limit_field,omitempty"`
}

type CapabilityProvider interface {
	Capabilities(model string) ModelCapabilities
}

type ProviderConfig struct {
	Name                string
	APIKey              string
	BaseURL             string
	Model               string
	AllowCustomEndpoint bool
	// AllowInsecure is for local contract tests only; CLI never enables it.
	AllowInsecure  bool
	HTTPClient     *http.Client
	RequestTimeout time.Duration
	// Nil uses two retries; zero explicitly disables retries.
	MaxRetries   *int
	Capabilities *ModelCapabilities
}

type ProviderSettings struct {
	Model, BaseURL, APIKeyEnv string
	Thinking                  bool
}

func DefaultProviderSettings(name string) ProviderSettings {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case ProviderDeepSeek, ProviderNone, "":
		return ProviderSettings{Model: DefaultModel, BaseURL: DefaultDeepSeekBaseURL, APIKeyEnv: "DEEPSEEK_API_KEY", Thinking: true} // #nosec G101 -- Public environment variable name, not a credential value.
	case ProviderOrcaRouter:
		return ProviderSettings{Model: "deepseek/" + DefaultModel, BaseURL: DefaultOrcaRouterBaseURL, APIKeyEnv: "ORCAROUTER_API_KEY", Thinking: true} // #nosec G101 -- Public environment variable name, not a credential value.
	case ProviderOpenAICompatible:
		return ProviderSettings{APIKeyEnv: "AEGIS_API_KEY"} // #nosec G101 -- Public environment variable name, not a credential value.
	default:
		return ProviderSettings{}
	}
}

func ProviderDisplayName(name string) string {
	switch name {
	case ProviderDeepSeek:
		return "DeepSeek"
	case ProviderOrcaRouter:
		return "OrcaRouter"
	default:
		return "OpenAI-compatible"
	}
}

func NewProvider(configuration ProviderConfig) (Provider, error) {
	configuration.Name = strings.ToLower(strings.TrimSpace(configuration.Name))
	if err := ValidateProvider(configuration.Name); err != nil {
		return nil, err
	}
	if configuration.Name == ProviderNone {
		return nil, nil
	}
	return NewOpenAICompatibleProvider(configuration)
}

func defaultCapabilities(provider, model string) ModelCapabilities {
	defaults := ModelCapabilities{Reasoning: "none", TokenLimitField: "max_tokens"}
	if provider == ProviderDeepSeek && (model == "deepseek-v4-flash" || model == "deepseek-v4-pro") {
		return ModelCapabilities{ToolCalling: true, JSONOutput: true, Reasoning: "deepseek", ReplayReasoning: true, TokenLimitField: "max_tokens"}
	}
	if provider == ProviderOrcaRouter && (model == "deepseek/deepseek-v4-flash" || model == "deepseek/deepseek-v4-pro") {
		return ModelCapabilities{ToolCalling: true, JSONOutput: true, Reasoning: "effort", ReplayReasoning: true, TokenLimitField: "max_tokens"}
	}
	return defaults
}

func normalizeCapabilities(capabilities ModelCapabilities) (ModelCapabilities, error) {
	if capabilities.Reasoning == "" {
		capabilities.Reasoning = "none"
	}
	switch capabilities.Reasoning {
	case "none", "effort", "deepseek":
	default:
		return ModelCapabilities{}, errors.New("capabilities.reasoning must be none, effort, or deepseek")
	}
	if capabilities.TokenLimitField == "" {
		capabilities.TokenLimitField = "max_tokens"
	}
	if capabilities.TokenLimitField != "max_tokens" && capabilities.TokenLimitField != "max_completion_tokens" {
		return ModelCapabilities{}, errors.New("capabilities.token_limit_field must be max_tokens or max_completion_tokens")
	}
	return capabilities, nil
}

func validateModel(model string) error {
	if strings.TrimSpace(model) == "" || len(model) > 128 || strings.ContainsAny(model, " \t\r\n") {
		return errors.New("model is required, must be at most 128 characters, and cannot contain whitespace")
	}
	for _, character := range model {
		if character < 0x21 || character > 0x7e {
			return errors.New("model must contain printable ASCII characters")
		}
	}
	return nil
}

// ProviderError intentionally excludes upstream messages, bodies, endpoints and
// credentials: upstream errors can echo entire requests into public PR reports.
type ProviderError struct {
	Provider   string
	Kind       string
	StatusCode int
	Retryable  bool
	cause      error
}

func (e *ProviderError) Error() string {
	if e.StatusCode > 0 {
		return fmt.Sprintf("%s API returned HTTP %d (%s)", ProviderDisplayName(e.Provider), e.StatusCode, e.Kind)
	}
	return fmt.Sprintf("%s completion failed (%s)", ProviderDisplayName(e.Provider), e.Kind)
}

func (e *ProviderError) Unwrap() error { return e.cause }

func classifyHTTPStatus(status int) string {
	switch status {
	case http.StatusUnauthorized:
		return "invalid_api_key"
	case http.StatusForbidden:
		return "permission_denied"
	case http.StatusTooManyRequests:
		return "rate_limited"
	case http.StatusRequestTimeout, http.StatusGatewayTimeout:
		return "timeout"
	default:
		if status >= 300 && status < 400 {
			return "redirect_rejected"
		}
		if status >= 500 {
			return "upstream_unavailable"
		}
		return "invalid_request"
	}
}
