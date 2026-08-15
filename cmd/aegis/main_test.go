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
	"github.com/Molly166/AegisCodeAgent/internal/report"
	"github.com/Molly166/AegisCodeAgent/internal/review"
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

func TestGitHubPublishesSummaryAnnotationsAndBlocksP1(t *testing.T) {
	directory := t.TempDir()
	reportPath := filepath.Join(directory, "review.json")
	htmlPath := filepath.Join(directory, "review.html")
	summaryPath := filepath.Join(directory, "summary.md")
	reviewReport := review.NewReport(review.Comparison{Base: "master", Head: "feature"}, []review.ChangedFile{{NewPath: "main.go"}}, []review.Finding{{
		Title: "Nil dereference", Description: "A nil value can reach this dereference.",
		Severity: review.SeverityHigh, Category: review.CategoryBug,
		Location: review.Location{Path: "main.go", StartLine: 12}, Source: "go-vet",
	}})
	reviewReport.Context = review.EmptyContextBundle(review.ContextComplete)
	encoded, err := report.RenderJSON(reviewReport)
	if err != nil {
		t.Fatal(err)
	}
	writeCLITestFile(t, reportPath, string(encoded))

	var stdout, stderr bytes.Buffer
	code := run(context.Background(), []string{
		"github", "--report", reportPath, "--html-output", htmlPath, "--summary", summaryPath,
		"--fail-on", "p1", "--artifact-name", "aegis-evidence",
	}, &stdout, &stderr)
	if code != 1 || !strings.Contains(stderr.String(), "blocked by P1") {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "::error ") || !strings.Contains(stdout.String(), "file=main.go,line=12") {
		t.Fatalf("missing GitHub annotation: %q", stdout.String())
	}
	for path, expected := range map[string]string{htmlPath: "AegisCodeAgent", summaryPath: "aegis-evidence"} {
		content, err := os.ReadFile(path)
		if err != nil || !strings.Contains(string(content), expected) {
			t.Fatalf("%s content=%q err=%v", path, content, err)
		}
	}
}

func TestGitHubAllowsCleanReportAndValidatesInputs(t *testing.T) {
	directory := t.TempDir()
	reportPath := filepath.Join(directory, "review.json")
	reviewReport := review.NewReport(review.Comparison{Base: "master", Head: "feature"}, nil, nil)
	encoded, err := report.RenderJSON(reviewReport)
	if err != nil {
		t.Fatal(err)
	}
	writeCLITestFile(t, reportPath, string(encoded))
	var stdout, stderr bytes.Buffer
	code := run(context.Background(), []string{
		"github", "--report", reportPath, "--html-output", filepath.Join(directory, "review.html"),
		"--summary", filepath.Join(directory, "summary.md"), "--annotations=false",
	}, &stdout, &stderr)
	if code != 0 || stdout.Len() != 0 || stderr.Len() != 0 {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}

	stdout.Reset()
	stderr.Reset()
	code = run(context.Background(), []string{"github", "--report", reportPath, "--summary", filepath.Join(directory, "summary-2.md"), "--fail-on", "critical"}, &stdout, &stderr)
	if code != 2 || !strings.Contains(stderr.String(), "unsupported priority") {
		t.Fatalf("code=%d stderr=%q", code, stderr.String())
	}
}

func TestLoadJSONReportRejectsUnknownSchemaAndTrailingData(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "review.json")
	for _, content := range []string{
		`{"schema_version":"v0"}`,
		`{"schema_version":"v6"} {}`,
		`{"schema_version":"v6","unknown":true}`,
	} {
		writeCLITestFile(t, path, content)
		if _, err := loadJSONReport(path); err == nil {
			t.Fatalf("loadJSONReport(%q) error = nil", content)
		}
	}
}

func TestLoadJSONReportSafelyMigratesV5(t *testing.T) {
	path := filepath.Join(t.TempDir(), "review.json")
	writeCLITestFile(t, path, `{"schema_version":"v5","verification":{"summary":{"candidates":1,"inconclusive":1},"candidates":[{"severity":"critical","verdict":"inconclusive"}]}}`)
	report, err := loadJSONReport(path)
	if err != nil {
		t.Fatal(err)
	}
	if report.SchemaVersion != review.SchemaVersion || report.Verification.Summary.NeedsReview != 1 || report.Verification.Candidates[0].Verdict != review.CandidateNeedsReview {
		t.Fatalf("v5 report was not migrated safely: %+v", report.Verification)
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

func TestReviewAndGitHubPublisherEndToEnd(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go is not installed")
	}
	repository := t.TempDir()
	runCLITestGit(t, repository, "init", "-b", "master")
	runCLITestGit(t, repository, "config", "user.name", "Aegis Test")
	runCLITestGit(t, repository, "config", "user.email", "aegis@example.com")
	writeCLITestFile(t, filepath.Join(repository, "go.mod"), "module example.com/aegisgithubfixture\n\ngo 1.23\n")
	writeCLITestFile(t, filepath.Join(repository, "main.go"), "package sample\n\nfunc Value() int { return 1 }\n")
	writeCLITestFile(t, filepath.Join(repository, "main_test.go"), "package sample\n\nimport \"testing\"\n\nfunc TestValue(t *testing.T) {\n\tif Value() != 1 { t.Fatal(\"wrong value\") }\n}\n")
	runCLITestGit(t, repository, "add", ".")
	runCLITestGit(t, repository, "commit", "-m", "base")
	base := strings.TrimSpace(runCLITestGit(t, repository, "rev-parse", "HEAD"))

	writeCLITestFile(t, filepath.Join(repository, "main_test.go"), "package sample\n\nimport \"testing\"\n\nfunc TestValue(t *testing.T) {\n\tt.Fatal(\"intentional regression\")\n}\n")
	runCLITestGit(t, repository, "add", "main_test.go")
	runCLITestGit(t, repository, "commit", "-m", "introduce failing regression test")

	reportPath := filepath.Join(repository, "review.json")
	var reviewStdout, reviewStderr bytes.Buffer
	if code := run(context.Background(), []string{
		"review", "--repo", repository, "--base", base, "--head", "HEAD",
		"--agent-provider", "none", "--format", "json", "--output", reportPath,
	}, &reviewStdout, &reviewStderr); code != 0 {
		t.Fatalf("review code=%d stdout=%q stderr=%q", code, reviewStdout.String(), reviewStderr.String())
	}

	summaryPath := filepath.Join(repository, "summary.md")
	htmlPath := filepath.Join(repository, "review.html")
	var publishStdout, publishStderr bytes.Buffer
	code := run(context.Background(), []string{
		"github", "--report", reportPath, "--html-output", htmlPath,
		"--summary", summaryPath, "--fail-on", "p1",
	}, &publishStdout, &publishStderr)
	if code != 1 || !strings.Contains(publishStdout.String(), "::error ") || !strings.Contains(publishStderr.String(), "blocked by P1") {
		t.Fatalf("publish code=%d stdout=%q stderr=%q", code, publishStdout.String(), publishStderr.String())
	}
	for path, expected := range map[string]string{summaryPath: "P1", htmlPath: "intentional regression"} {
		content, err := os.ReadFile(path)
		if err != nil || !strings.Contains(string(content), expected) {
			t.Fatalf("%s content=%q err=%v", path, content, err)
		}
	}
}

func TestSemanticCredentialLeakIsDetectedAndBlockedWithoutAgent(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go is not installed")
	}
	repository := t.TempDir()
	runCLITestGit(t, repository, "init", "-b", "master")
	runCLITestGit(t, repository, "config", "user.name", "Aegis Test")
	runCLITestGit(t, repository, "config", "user.email", "aegis@example.com")
	writeCLITestFile(t, filepath.Join(repository, "go.mod"), "module example.com/semanticfixture\n\ngo 1.23\n")
	baseSource := `package sample

import "os/exec"

func ForUntrustedChild(environment []string) []string { return environment }
func Run() *exec.Cmd {
	return exec.Command("helper")
}
`
	writeCLITestFile(t, filepath.Join(repository, "main.go"), baseSource)
	runCLITestGit(t, repository, "add", ".")
	runCLITestGit(t, repository, "commit", "-m", "base")
	base := strings.TrimSpace(runCLITestGit(t, repository, "rev-parse", "HEAD"))

	buggySource := `package sample

import (
	"os"
	"os/exec"
)

func ForUntrustedChild(environment []string) []string { return environment }
func Run() *exec.Cmd {
	process := exec.Command("helper")
	process.Env = append(ForUntrustedChild(os.Environ()), "AEGIS_REVIEW_TOKEN="+os.Getenv("DEEPSEEK_API_KEY"))
	return process
}
`
	writeCLITestFile(t, filepath.Join(repository, "main.go"), buggySource)
	runCLITestGit(t, repository, "add", "main.go")
	runCLITestGit(t, repository, "commit", "-m", "introduce credential leak")

	reportPath := filepath.Join(repository, "review.json")
	var reviewStdout, reviewStderr bytes.Buffer
	code := run(context.Background(), []string{
		"review", "--repo", repository, "--base", base, "--head", "HEAD",
		"--agent-provider", "none", "--format", "json", "--output", reportPath,
	}, &reviewStdout, &reviewStderr)
	if code != 0 {
		t.Fatalf("review code=%d stdout=%q stderr=%q", code, reviewStdout.String(), reviewStderr.String())
	}
	reviewReport, err := loadJSONReport(reportPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(reviewReport.Findings) != 1 || reviewReport.Findings[0].RuleID != "AEGIS-SEC-001" || reviewReport.Findings[0].Severity != review.SeverityCritical {
		t.Fatalf("semantic P0 was not emitted: %+v", reviewReport.Findings)
	}

	var publishStdout, publishStderr bytes.Buffer
	summaryPath := filepath.Join(repository, "summary.md")
	code = run(context.Background(), []string{
		"github", "--report", reportPath, "--html-output", filepath.Join(repository, "review.html"),
		"--summary", summaryPath, "--fail-on", "p1", "--fail-on-needs-review", "p0",
	}, &publishStdout, &publishStderr)
	if code != 1 || !strings.Contains(publishStderr.String(), "blocked by P0") {
		t.Fatalf("publisher did not block semantic P0: code=%d stdout=%q stderr=%q", code, publishStdout.String(), publishStderr.String())
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
