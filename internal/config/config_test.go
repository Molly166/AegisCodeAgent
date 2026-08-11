package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadAndRejectUnknownFields(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "config.json")
	if err := os.WriteFile(path, []byte(`{"version":1,"agent":{"provider":"deepseek","thinking":true},"verifier":{"enabled":true,"timeout":"3m","analyzer_timeout":"2m"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Agent.Provider != "deepseek" || loaded.Agent.Thinking == nil || !*loaded.Agent.Thinking {
		t.Fatalf("unexpected config: %+v", loaded)
	}
	if loaded.Verifier.Enabled == nil || !*loaded.Verifier.Enabled || loaded.Verifier.Timeout != "3m" || loaded.Verifier.AnalyzerTimeout != "2m" {
		t.Fatalf("unexpected verifier config: %+v", loaded.Verifier)
	}

	if err := os.WriteFile(path, []byte(`{"version":1,"unknown":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("Load() error = %v, want unknown field", err)
	}
}

func TestLoadRejectsVersionAndTrailingJSON(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "config.json")
	for _, content := range []string{
		`{"version":2,"agent":{}}`,
		`{"version":1,"agent":{}} {"version":1}`,
	} {
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := Load(path); err == nil {
			t.Fatalf("Load(%s) error = nil", content)
		}
	}
}

func TestAPIKeyPrefersEnvironmentAndReadsDotEnv(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, ".env")
	if err := os.WriteFile(path, []byte("OTHER=x\nDEEPSEEK_API_KEY='file-secret'\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DEEPSEEK_API_KEY", "environment-secret")
	value, err := APIKey(path, "DEEPSEEK_API_KEY")
	if err != nil || value != "environment-secret" {
		t.Fatalf("value=%q err=%v", value, err)
	}

	if err := os.Unsetenv("DEEPSEEK_API_KEY"); err != nil {
		t.Fatal(err)
	}
	value, err = APIKey(path, "DEEPSEEK_API_KEY")
	if err != nil || value != "file-secret" {
		t.Fatalf("value=%q err=%v", value, err)
	}
}

func TestAPIKeyValidatesNameAndHandlesMissingFile(t *testing.T) {
	t.Setenv("DEEPSEEK_API_KEY", "")
	if _, err := APIKey(filepath.Join(t.TempDir(), ".env"), "INVALID-NAME"); err == nil {
		t.Fatal("invalid environment name was accepted")
	}
	value, err := APIKey(filepath.Join(t.TempDir(), ".env"), "DEEPSEEK_API_KEY")
	if err != nil || value != "" {
		t.Fatalf("value=%q err=%v", value, err)
	}
}
