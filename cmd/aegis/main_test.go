package main

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	appconfig "github.com/Molly166/AegisCodeAgent/internal/config"
)

func TestRunVersion(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run(context.Background(), []string{"version"}, &stdout, &stderr)
	if code != 0 || stdout.String() != "aegis "+version+"\n" || stderr.Len() != 0 {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}

func TestRunUnknownCommand(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run(context.Background(), []string{"unknown"}, &stdout, &stderr)
	if code != 2 || !strings.Contains(stderr.String(), "unknown command") {
		t.Fatalf("code=%d stderr=%q", code, stderr.String())
	}
}

func TestReviewRejectsUnsupportedFormatBeforeCollection(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run(context.Background(), []string{
		"review", "--repo", "/path/that/does/not/exist", "--format", "xml",
	}, &stdout, &stderr)
	if code != 2 || !strings.Contains(stderr.String(), "unsupported report format") {
		t.Fatalf("code=%d stderr=%q", code, stderr.String())
	}
}

func TestReviewRequiresDeepSeekCredentialBeforeCollection(t *testing.T) {
	t.Setenv("DEEPSEEK_API_KEY", "")
	var stdout, stderr bytes.Buffer
	code := run(context.Background(), []string{
		"review", "--repo", "/path/that/does/not/exist", "--agent-provider", "deepseek",
	}, &stdout, &stderr)
	if code != 2 || !strings.Contains(stderr.String(), "DeepSeek API key is missing") {
		t.Fatalf("code=%d stderr=%q", code, stderr.String())
	}
}

func TestReviewLoadsExplicitConfigOnly(t *testing.T) {
	directory := t.TempDir()
	configPath := filepath.Join(directory, "aegis.json")
	writeCLITestFile(t, configPath, `{"version":1,"agent":{"provider":"invalid-provider"}}`)
	var stdout, stderr bytes.Buffer
	code := run(context.Background(), []string{
		"review", "--repo", "/path/that/does/not/exist", "--config", configPath,
	}, &stdout, &stderr)
	if code != 2 || !strings.Contains(stderr.String(), "unsupported agent provider") {
		t.Fatalf("code=%d stderr=%q", code, stderr.String())
	}
}

func TestReviewVerifierDefaults(t *testing.T) {
	enabled := false
	defaults, err := reviewVerifierDefaults(appconfig.VerifierConfig{
		Enabled: &enabled, Timeout: "45s", AnalyzerTimeout: "20s",
	})
	if err != nil {
		t.Fatal(err)
	}
	if defaults.Enabled || defaults.Timeout != 45*time.Second || defaults.AnalyzerTimeout != 20*time.Second {
		t.Fatalf("unexpected verifier defaults: %+v", defaults)
	}
	for _, configuration := range []appconfig.VerifierConfig{{Timeout: "invalid"}, {AnalyzerTimeout: "invalid"}} {
		if _, err := reviewVerifierDefaults(configuration); err == nil {
			t.Fatalf("invalid verifier duration was accepted: %+v", configuration)
		}
	}
}

func TestReviewRunsGoVetAndWritesHTMLFinding(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go is not installed")
	}
	repository := t.TempDir()
	runCLITestGit(t, repository, "init", "-b", "main")
	runCLITestGit(t, repository, "config", "user.name", "Aegis Test")
	runCLITestGit(t, repository, "config", "user.email", "aegis@example.com")
	writeCLITestFile(t, filepath.Join(repository, "go.mod"), "module example.com/aegisfixture\n\ngo 1.23\n")
	writeCLITestFile(t, filepath.Join(repository, "main.go"), "package main\n\nimport \"fmt\"\n\nfunc main() { fmt.Printf(\"%d\", 1) }\n")
	runCLITestGit(t, repository, "add", ".")
	runCLITestGit(t, repository, "commit", "-m", "base")
	base := strings.TrimSpace(runCLITestGit(t, repository, "rev-parse", "HEAD"))

	writeCLITestFile(t, filepath.Join(repository, "main.go"), "package main\n\nimport \"fmt\"\n\nfunc main() { fmt.Printf(\"%d\", \"wrong\") }\n")
	runCLITestGit(t, repository, "add", "main.go")
	runCLITestGit(t, repository, "commit", "-m", "introduce vet diagnostic")

	outputPath := filepath.Join(repository, "review.html")
	var stdout, stderr bytes.Buffer
	code := run(context.Background(), []string{
		"review",
		"--repo", repository,
		"--base", base,
		"--head", "HEAD",
		"--analyzers", "go-test,go-vet",
		"--output", outputPath,
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	output, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatal(err)
	}
	html := string(output)
	for _, expected := range []string{"Analyzer execution", "go-vet", "FINDINGS", "fmt.Printf", "main.go", "Repository context", "Changed symbols", "Token estimate"} {
		if !strings.Contains(html, expected) {
			t.Errorf("HTML does not contain %q", expected)
		}
	}
}

func writeCLITestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func runCLITestGit(t *testing.T, repository string, arguments ...string) string {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", repository}, arguments...)...)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", arguments, err, output)
	}
	return string(output)
}
