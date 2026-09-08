package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Molly166/AegisCodeAgent/internal/agent"
	appconfig "github.com/Molly166/AegisCodeAgent/internal/config"
)

func TestProviderCLIOrcaUsesItsOwnDefaults(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run(context.Background(), []string{"review", "--agent-provider", "orcarouter", "--help"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("help failed: %s", stderr.String())
	}
	for _, value := range []string{"ORCAROUTER_API_KEY", "https://api.orcarouter.ai/v1", "deepseek/deepseek-v4-flash"} {
		if !strings.Contains(stderr.String(), value) {
			t.Errorf("missing provider default %q in help: %s", value, stderr.String())
		}
	}
	if strings.Contains(stderr.String(), `default "DEEPSEEK_API_KEY"`) || strings.Contains(stderr.String(), `default "https://api.deepseek.com"`) {
		t.Fatalf("Orca inherited DeepSeek credentials/endpoint: %s", stderr.String())
	}
}

func TestProviderCLIOverrideDoesNotInheritConfiguredEndpointKeyOrModel(t *testing.T) {
	directory := t.TempDir()
	configPath := filepath.Join(directory, "aegis.json")
	writeCLITestFile(t, configPath, `{"version":1,"agent":{"provider":"deepseek","model":"old-provider-model","base_url":"https://old-provider.example/v1","api_key_env":"OLD_PROVIDER_KEY","thinking":true,"reasoning_effort":"max","capabilities":{"reasoning":"invalid-old-profile"}}}`)
	var stdout, stderr bytes.Buffer
	code := run(context.Background(), []string{"review", "--config", configPath, "--agent-provider", "orcarouter", "--help"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("provider override help failed: %s", stderr.String())
	}
	for _, old := range []string{"old-provider-model", "https://old-provider.example/v1", "OLD_PROVIDER_KEY"} {
		if strings.Contains(stderr.String(), old) {
			t.Errorf("provider override retained %s", old)
		}
	}
	for _, expected := range []string{"deepseek/deepseek-v4-flash", "https://api.orcarouter.ai/v1", "ORCAROUTER_API_KEY"} {
		if !strings.Contains(stderr.String(), expected) {
			t.Errorf("provider override lost %s", expected)
		}
	}
	t.Setenv("ORCAROUTER_API_KEY", "test-provider-key")
	stdout.Reset()
	stderr.Reset()
	code = run(context.Background(), []string{"review", "--repo", filepath.Join(directory, "missing"), "--config", configPath, "--agent-provider", "orcarouter", "--agent-no-dotenv"}, &stdout, &stderr)
	if code != 1 || strings.Contains(stderr.String(), "capabilities.reasoning") || strings.Contains(stderr.String(), "credential") {
		t.Fatalf("provider switch retained old capability/key configuration: code=%d err=%s", code, stderr.String())
	}
}

func TestProviderCLISwitchModelDoesNotReuseOldCapabilityContract(t *testing.T) {
	directory := t.TempDir()
	configPath := filepath.Join(directory, "aegis.json")
	writeCLITestFile(t, configPath, `{"version":1,"agent":{"provider":"openai-compatible","model":"old-model","base_url":"https://provider.example/v1","thinking":true,"capabilities":{"reasoning":"invalid-old-model-profile"}}}`)
	t.Setenv("AEGIS_API_KEY", "test-provider-key")
	var stdout, stderr bytes.Buffer
	code := run(context.Background(), []string{"review", "--repo", filepath.Join(directory, "missing"), "--config", configPath, "--agent-model", "new-model", "--agent-allow-custom-endpoint", "--agent-no-dotenv"}, &stdout, &stderr)
	// Construction must pass and reach local Git validation. No model request is
	// sent because the repository does not exist. Reusing the old profile fails
	// during provider construction, which exposes this boundary regression.
	if code != 1 || strings.Contains(stderr.String(), "capabilities.reasoning") {
		t.Fatalf("model switch reused old model profile: code=%d err=%s", code, stderr.String())
	}
}

func TestProviderCLINoDotEnvNeverLoadsRepositoryOrConfigDirectoryKeys(t *testing.T) {
	for _, useConfig := range []bool{false, true} {
		t.Run(map[bool]string{false: "repository", true: "config directory"}[useConfig], func(t *testing.T) {
			directory := t.TempDir()
			repository := filepath.Join(directory, "repo")
			if err := os.Mkdir(repository, 0o700); err != nil {
				t.Fatal(err)
			}
			keyDirectory := repository
			arguments := []string{"review", "--repo", repository, "--agent-provider", "deepseek"}
			if useConfig {
				keyDirectory = directory
				configPath := filepath.Join(directory, "aegis.json")
				writeCLITestFile(t, configPath, `{"version":1,"agent":{"provider":"deepseek"}}`)
				arguments = append(arguments, "--config", configPath)
			}
			writeCLITestFile(t, filepath.Join(keyDirectory, ".env"), "DEEPSEEK_API_KEY=dotenv-secret\n")
			t.Setenv("DEEPSEEK_API_KEY", "")
			var stdout, stderr bytes.Buffer
			code := run(context.Background(), append(arguments, "--agent-no-dotenv"), &stdout, &stderr)
			if code != 2 || !strings.Contains(stderr.String(), "DeepSeek API key is missing") || strings.Contains(stderr.String(), "dotenv-secret") {
				t.Fatalf("dotenv loaded in env-only mode: %d %s", code, stderr.String())
			}
			stdout.Reset()
			stderr.Reset()
			code = run(context.Background(), arguments, &stdout, &stderr)
			if code != 1 || strings.Contains(stderr.String(), "API key is missing") {
				t.Fatalf("explicit local dotenv support lost: %d %s", code, stderr.String())
			}
		})
	}
}

func TestProviderCLIGenericRequiresExplicitModelAndEndpointOptIn(t *testing.T) {
	t.Setenv("AEGIS_API_KEY", "test-provider-key")
	for _, test := range []struct {
		arguments []string
		want      string
	}{
		{[]string{"--agent-base-url", "https://provider.example/v1", "--agent-allow-custom-endpoint"}, "requires an explicit model"},
		{[]string{"--agent-model", "chosen-model", "--agent-base-url", "https://provider.example/v1"}, "explicit opt-in"},
		{[]string{"--agent-model", "chosen-model", "--agent-allow-custom-endpoint"}, "base URL"},
	} {
		var stdout, stderr bytes.Buffer
		arguments := append([]string{"review", "--repo", "/path/that/does/not/exist", "--agent-provider", "openai-compatible", "--agent-no-dotenv"}, test.arguments...)
		code := run(context.Background(), arguments, &stdout, &stderr)
		if code != 2 || !strings.Contains(stderr.String(), test.want) {
			t.Errorf("arguments=%v code=%d stderr=%s", test.arguments, code, stderr.String())
		}
	}
}

func TestProviderCredentialSelectionCannotReadUnrelatedSecrets(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "unrelated-secret")
	for _, provider := range []string{agent.ProviderDeepSeek, agent.ProviderOrcaRouter, agent.ProviderOpenAICompatible} {
		_, err := configureReasoningProvider(t.TempDir(), "", agent.ProviderConfig{Name: provider}, "GITHUB_TOKEN", false)
		if err == nil || !strings.Contains(err.Error(), "only accepts") || strings.Contains(err.Error(), "unrelated-secret") {
			t.Fatalf("unrelated secret lookup allowed: %s %v", provider, err)
		}
	}
}

func TestProviderDefaultAndRetryConfig(t *testing.T) {
	zero := 0
	defaults, err := reviewAgentDefaults(appconfig.AgentConfig{Provider: "orcarouter", RequestTimeout: "45s", MaxRetries: &zero})
	if err != nil || defaults.RequestTimeout != 45*time.Second || defaults.MaxRetries != 0 || defaults.Model != "deepseek/deepseek-v4-flash" {
		t.Fatalf("incorrect config defaults: %+v %v", defaults, err)
	}
	for _, duration := range []string{"bad", "0s", "-1s", "11m"} {
		if _, err := providerRequestTimeout(duration); err == nil {
			t.Errorf("invalid request timeout accepted: %s", duration)
		}
	}
}
