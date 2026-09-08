package main

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/Molly166/AegisCodeAgent/internal/agent"
	appconfig "github.com/Molly166/AegisCodeAgent/internal/config"
)

// configureReasoningProvider keeps secret loading out of the review pipeline.
// Workflows disable dotenv loading to prevent a PR checkout controlling keys.
func configureReasoningProvider(repository, configPath string, configuration agent.ProviderConfig, keyEnv string, readDotEnv bool) (agent.Provider, error) {
	configuration.Name = strings.ToLower(strings.TrimSpace(configuration.Name))
	if err := agent.ValidateProvider(configuration.Name); err != nil {
		return nil, err
	}
	if configuration.Name == agent.ProviderNone {
		return nil, nil
	}
	defaults := agent.DefaultProviderSettings(configuration.Name)
	if keyEnv == "" {
		keyEnv = defaults.APIKeyEnv
	}
	// Restrict credential lookup to the provider's dedicated variable; a config
	// must never turn GITHUB_TOKEN or an unrelated environment secret into a key.
	if keyEnv != defaults.APIKeyEnv {
		return nil, fmt.Errorf("%s provider only accepts %s as its credential variable", agent.ProviderDisplayName(configuration.Name), defaults.APIKeyEnv)
	}
	dotEnvPath := ""
	if readDotEnv {
		dotEnvPath = filepath.Join(repository, ".env")
		if configPath != "" {
			dotEnvPath = filepath.Join(filepath.Dir(configPath), ".env")
		}
	}
	apiKey, err := appconfig.APIKey(dotEnvPath, keyEnv)
	if err != nil {
		return nil, err
	}
	if apiKey == "" {
		if readDotEnv {
			return nil, fmt.Errorf("%s API key is missing; set %s or add it to %s", agent.ProviderDisplayName(configuration.Name), keyEnv, dotEnvPath)
		}
		return nil, fmt.Errorf("%s API key is missing; set %s", agent.ProviderDisplayName(configuration.Name), keyEnv)
	}
	configuration.APIKey = apiKey
	return agent.NewProvider(configuration)
}

func configuredCapabilities(configuration *appconfig.ModelCapabilities) *agent.ModelCapabilities {
	if configuration == nil {
		return nil
	}
	return &agent.ModelCapabilities{
		ToolCalling: configuration.ToolCalling, JSONOutput: configuration.JSONOutput,
		Reasoning: configuration.Reasoning, ReplayReasoning: configuration.ReplayReasoning,
		TokenLimitField: configuration.TokenLimitField,
	}
}

func providerRequestTimeout(value string) (time.Duration, error) {
	if value == "" {
		return 3 * time.Minute, nil
	}
	duration, err := time.ParseDuration(value)
	if err != nil || duration < time.Millisecond || duration > 10*time.Minute {
		return 0, fmt.Errorf("agent request_timeout must be a duration between 1ms and 10m")
	}
	return duration, nil
}
