package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Molly166/AegisCodeAgent/internal/report"
	"github.com/Molly166/AegisCodeAgent/internal/review"
)

func gateOutputReport() review.ReviewReport {
	value := review.NewReport(review.Comparison{
		Base: "master", Head: "feature", BaseCommit: strings.Repeat("a", 40), HeadCommit: strings.Repeat("b", 40),
	}, []review.ChangedFile{{NewPath: "main.go"}}, nil)
	value.Context = review.EmptyContextBundle(review.ContextComplete)
	value.Agent = review.EmptyAgentRun(review.AgentComplete)
	value.Verification = review.EmptyVerificationRun(review.VerificationComplete)
	return value
}

func writeGateOutputReport(t *testing.T, directory string, value review.ReviewReport) string {
	t.Helper()
	encoded, err := report.RenderJSON(value)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(directory, "review.json")
	writeCLITestFile(t, path, string(encoded))
	return path
}

func gateOutputArguments(directory, input, base, head string) []string {
	return []string{
		"github", "--report", input, "--html-output", filepath.Join(directory, "review.html"),
		"--summary", filepath.Join(directory, "summary.md"), "--gate-output", filepath.Join(directory, "gate.json"),
		"--expected-base", base, "--expected-head", head, "--fail-on", "p1", "--fail-on-needs-review", "p0", "--annotations=false",
	}
}

func TestGitHubGateOutputReflectsTheOriginalMergeDecision(t *testing.T) {
	tests := []struct {
		name           string
		severity       review.Severity
		agentPartial   bool
		analysisStatus review.AnalysisStatus
		requireAgent   bool
		wantCode       int
		wantBlocked    bool
		wantIncomplete bool
		wantDegraded   bool
	}{
		{name: "clean report is passed"},
		{name: "P1 finding is blocked and still writes gate JSON", severity: review.SeverityHigh, wantCode: 1, wantBlocked: true},
		{name: "P2 is advisory and remains passed", severity: review.SeverityMedium},
		{name: "optional partial reasoning is degraded rather than blocked", agentPartial: true, wantDegraded: true},
		{name: "required analysis incomplete remains blocked", analysisStatus: review.AnalysisPartial, wantCode: 1, wantBlocked: true, wantIncomplete: true},
		{name: "required reasoning preserves its stricter original policy", agentPartial: true, requireAgent: true, wantCode: 1, wantBlocked: true, wantIncomplete: true, wantDegraded: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			directory := t.TempDir()
			value := gateOutputReport()
			if test.severity != "" {
				value.Findings = []review.Finding{{
					Title: "Static review diagnostic", Description: "Independent analyzer evidence.",
					Severity: test.severity, Category: review.CategoryBug,
					Location: review.Location{Path: "main.go", StartLine: 12}, Source: "go-vet",
				}}
				value.RecalculateSummary()
			}
			if test.agentPartial {
				value.Agent = review.EmptyAgentRun(review.AgentPartial)
			}
			if test.analysisStatus != "" {
				value.Analysis.Status = test.analysisStatus
			}
			input := writeGateOutputReport(t, directory, value)
			arguments := gateOutputArguments(directory, input, value.Comparison.BaseCommit, value.Comparison.HeadCommit)
			if test.requireAgent {
				arguments = append(arguments, "--require-agent=true")
			}
			var stdout, stderr bytes.Buffer
			code := run(context.Background(), arguments, &stdout, &stderr)
			if code != test.wantCode {
				t.Fatalf("exit=%d, want %d; stderr=%s", code, test.wantCode, stderr.String())
			}
			contents, err := os.ReadFile(filepath.Join(directory, "gate.json"))
			if err != nil {
				t.Fatalf("gate decision was not written for exit %d: %v", code, err)
			}
			var decoded map[string]any
			if err := json.Unmarshal(contents, &decoded); err != nil {
				t.Fatalf("gate decision is not JSON: %v", err)
			}
			want := map[string]any{
				"schema_version": float64(1), "base": value.Comparison.BaseCommit, "head": value.Comparison.HeadCommit,
				"blocked": test.wantBlocked, "incomplete": test.wantIncomplete, "degraded": test.wantDegraded, "needs_review": false,
			}
			for key, expected := range want {
				if actual, exists := decoded[key]; !exists || actual != expected {
					t.Errorf("gate[%q]=%v (present=%v), want %v; JSON=%s", key, actual, exists, expected, contents)
				}
			}
			for _, name := range []string{"review.html", "summary.md"} {
				if content, err := os.ReadFile(filepath.Join(directory, name)); err != nil || len(content) == 0 {
					t.Errorf("machine decision must not replace evidence output %s: err=%v", name, err)
				}
			}
		})
	}
}

func TestGitHubGateOutputRejectsMismatchedEvidenceBeforeWriting(t *testing.T) {
	for _, changed := range []string{"base", "head"} {
		t.Run(changed, func(t *testing.T) {
			directory := t.TempDir()
			value := gateOutputReport()
			input := writeGateOutputReport(t, directory, value)
			base, head := value.Comparison.BaseCommit, value.Comparison.HeadCommit
			if changed == "base" {
				base = strings.Repeat("c", 40)
			} else {
				head = strings.Repeat("c", 40)
			}
			var stdout, stderr bytes.Buffer
			code := run(context.Background(), gateOutputArguments(directory, input, base, head), &stdout, &stderr)
			if code == 0 {
				t.Fatal("stale comparison was accepted")
			}
			for _, name := range []string{"gate.json", "review.html", "summary.md"} {
				if _, err := os.Lstat(filepath.Join(directory, name)); !errors.Is(err, os.ErrNotExist) {
					t.Errorf("mismatched evidence produced %s or an unexpected error: %v", name, err)
				}
			}
		})
	}
}

func TestGitHubGateOutputWriteFailureCannotExitSuccessfully(t *testing.T) {
	directory := t.TempDir()
	value := gateOutputReport()
	input := writeGateOutputReport(t, directory, value)
	// A directory is reliably unwritable as a JSON file even under a privileged
	// test process; chmod-only fixtures are not portable permission checks.
	gatePath := filepath.Join(directory, "gate.json")
	if err := os.Mkdir(gatePath, 0700); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	code := run(context.Background(), gateOutputArguments(directory, input, value.Comparison.BaseCommit, value.Comparison.HeadCommit), &stdout, &stderr)
	if code == 0 || stderr.Len() == 0 {
		t.Fatalf("failed gate output was silently accepted: code=%d stderr=%q", code, stderr.String())
	}
	info, err := os.Lstat(gatePath)
	if err != nil || !info.IsDir() {
		t.Fatalf("gate failure unexpectedly replaced the existing target: %v", err)
	}
}
