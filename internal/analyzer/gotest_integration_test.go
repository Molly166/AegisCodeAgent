package analyzer

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Molly166/AegisCodeAgent/internal/review"
)

// Exercise the real go test JSON protocol: hand-written events alone can miss
// differences between successful logs, skipped tests, and compiler output.
func TestGoTestAnalyzerRealProcess(t *testing.T) {
	goTestOfflineEnvironment(t)

	cases := []struct {
		name         string
		source       string
		wantExitCode int
		wantFindings int
		wantEvidence string
		wantLocated  bool
	}{
		{
			name: "successful logs and skipped test are not findings",
			source: `package fixture
import "testing"
func TestPassing(t *testing.T) { t.Log("passing-log-canary") }
func TestSkipped(t *testing.T) { t.Skip("skip-reason-canary") }
`,
			wantExitCode: 0,
			wantFindings: 0,
		},
		{
			name: "mixed test outcomes only report the failed test",
			source: `package fixture
import "testing"
func TestPassing(t *testing.T) { t.Log("passing-log-canary") }
func TestSkipped(t *testing.T) { t.Skip("skip-reason-canary") }
func TestFailing(t *testing.T) { t.Error("failed-assertion-canary") }
`,
			wantExitCode: 1,
			wantFindings: 1,
			wantEvidence: "failed-assertion-canary",
			wantLocated:  true,
		},
		{
			name: "compiler failure remains a finding",
			source: `package fixture
import "testing"
func TestBuild(t *testing.T) { missingBuildSymbol() }
`,
			wantExitCode: 1,
			wantFindings: 1,
			wantEvidence: "missingBuildSymbol",
			wantLocated:  true,
		},
		{
			name: "several logs in one failed test do not multiply findings",
			source: `package fixture
import "testing"
func TestFailing(t *testing.T) {
 t.Log("first diagnostic context")
 t.Log("second diagnostic context")
 t.Log("third diagnostic context")
 t.Error("failed-assertion-canary")
}
`,
			wantExitCode: 1,
			wantFindings: 1,
			wantEvidence: "failed-assertion-canary",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repository := t.TempDir()
			for name, contents := range map[string]string{
				"go.mod":          "module example.com/aegis/gotestfixture\n\ngo 1.23.0\n",
				"fixture_test.go": tc.source,
			} {
				if err := os.WriteFile(filepath.Join(repository, name), []byte(contents), 0600); err != nil {
					t.Fatal(err)
				}
			}

			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			defer cancel()
			runner := &goTestRecordingOSRunner{}
			findings, err := NewGoTestAnalyzer(runner).Analyze(ctx, Input{Repository: repository, Packages: []string{"."}})
			if err != nil {
				t.Fatalf("Analyze() error = %v\n%s", err, runner.execution.CombinedOutput())
			}
			if runner.execution.ExitCode != tc.wantExitCode {
				t.Fatalf("go test exit code = %d, want %d\n%s", runner.execution.ExitCode, tc.wantExitCode, runner.execution.CombinedOutput())
			}
			if len(findings) != tc.wantFindings {
				t.Fatalf("finding count = %d, want %d: %+v\n%s", len(findings), tc.wantFindings, findings, runner.execution.CombinedOutput())
			}
			for _, finding := range findings {
				if finding.Source != NameGoTest || finding.Severity != review.SeverityHigh {
					t.Errorf("unexpected failure classification: %+v", finding)
				}
				if !strings.Contains(finding.Evidence, tc.wantEvidence) {
					t.Errorf("failure evidence does not contain %q: %+v", tc.wantEvidence, finding)
				}
				if strings.Contains(finding.Evidence, "passing-log-canary") || strings.Contains(finding.Evidence, "skip-reason-canary") {
					t.Errorf("failure evidence includes passing or skipped test output: %+v", finding)
				}
				if tc.wantLocated {
					if finding.Location.Path != "fixture_test.go" || finding.Location.StartLine <= 0 {
						t.Errorf("failure should retain its unambiguous source location: %+v", finding)
					}
				} else if finding.Location != (review.Location{}) {
					// Test2json does not identify which of several logged source
					// positions is an assertion; retain proof without guessing.
					t.Errorf("ambiguous log positions must not invent an assertion location: %+v", finding)
				}
			}
		})
	}
}

func TestGoTestAnalyzerRealNestedPackageBuildFailure(t *testing.T) {
	goTestOfflineEnvironment(t)
	repository := t.TempDir()
	if err := os.Mkdir(filepath.Join(repository, "sub"), 0700); err != nil {
		t.Fatal(err)
	}
	for name, contents := range map[string]string{
		"go.mod": "module example.com/aegis/gotestfixture\n\ngo 1.23.0\n",
		"sub/fixture_test.go": `package fixture
import "testing"
func TestBuild(t *testing.T) { missingNestedBuildSymbol() }
`,
	} {
		if err := os.WriteFile(filepath.Join(repository, name), []byte(contents), 0600); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	runner := &goTestRecordingOSRunner{}
	findings, err := NewGoTestAnalyzer(runner).Analyze(ctx, Input{Repository: repository, Packages: []string{"./sub"}})
	if err != nil {
		t.Fatalf("Analyze() error = %v\n%s", err, runner.execution.CombinedOutput())
	}
	if runner.execution.ExitCode != 1 || len(findings) != 1 {
		t.Fatalf("nested build failure: exit=%d findings=%+v\n%s", runner.execution.ExitCode, findings, runner.execution.CombinedOutput())
	}
	finding := findings[0]
	if finding.Location.Path != "sub/fixture_test.go" || finding.Location.StartLine != 3 {
		t.Errorf("compiler source path must not repeat the package directory: %+v\n%s", finding, runner.execution.CombinedOutput())
	}
	if finding.Source != NameGoTest || finding.Severity != review.SeverityHigh || !strings.Contains(finding.Evidence, "missingNestedBuildSymbol") {
		t.Errorf("nested compiler failure lost its classification or evidence: %+v", finding)
	}
}

func TestGoTestAnalyzerRejectsSuccessfulProcessWithoutJSONEvidence(t *testing.T) {
	for _, tc := range []struct{ name, output string }{
		{name: "empty output"},
		{name: "non JSON output", output: "ordinary output without test2json events\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			runner := &fakeRunner{
				available:  map[string]bool{"go": true},
				executions: map[string]Execution{"go": {Stdout: tc.output, ExitCode: 0}},
			}
			findings, err := NewGoTestAnalyzer(runner).Analyze(context.Background(), Input{Repository: t.TempDir(), Packages: []string{"."}})
			if err == nil {
				t.Fatal("exit code 0 without terminal package evidence must remain incomplete")
			}
			if len(findings) != 0 {
				t.Fatalf("missing evidence is not a confirmed failure: %+v", findings)
			}
		})
	}
}

func goTestOfflineEnvironment(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go is unavailable for the real-process regression test")
	}
	// These fixtures import only the standard library and must never fetch a
	// dependency, workspace module, or alternate toolchain from the network.
	for name, value := range map[string]string{
		"GOPROXY":     "off",
		"GOSUMDB":     "off",
		"GOWORK":      "off",
		"GOTOOLCHAIN": "local",
		"GOENV":       "off",
		"GOFLAGS":     "",
		"CGO_ENABLED": "0",
	} {
		t.Setenv(name, value)
	}
}

// Record the subprocess result without replacing the production runner or
// manufacturing any test2json events.
type goTestRecordingOSRunner struct {
	OSRunner
	execution Execution
}

func (r *goTestRecordingOSRunner) Run(ctx context.Context, command Command) (Execution, error) {
	execution, err := r.OSRunner.Run(ctx, command)
	r.execution = execution
	return execution, err
}
