package verifier

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Molly166/AegisCodeAgent/internal/analyzer"
	"github.com/Molly166/AegisCodeAgent/internal/review"
)

type fakePipeline struct {
	output analyzer.Output
	err    error
	calls  int
	input  analyzer.Input
	names  []string
}

func (p *fakePipeline) Run(_ context.Context, input analyzer.Input, names []string, _ analyzer.RunOptions) (analyzer.Output, error) {
	p.calls++
	p.input = input
	p.names = append([]string(nil), names...)
	return p.output, p.err
}

func TestVerifierLinksCandidateToExistingFindingWithoutDuplication(t *testing.T) {
	repository, files, candidate := verificationFixture(t)
	existing := diagnosticFixture("STATIC-1")
	pipeline := &fakePipeline{output: analyzer.Output{Analysis: completeFocusedAnalysis()}}

	output, err := New(pipeline).Run(context.Background(), Config{Repository: repository, AnalyzerTimeout: time.Second}, Input{
		Files: files, Findings: []review.Finding{existing}, Agent: agentWith(candidate),
	})
	if err != nil {
		t.Fatal(err)
	}
	if output.Verification.Status != review.VerificationComplete || output.Verification.Summary.Verified != 1 {
		t.Fatalf("unexpected verification: %+v", output.Verification)
	}
	result := output.Verification.Candidates[0]
	if result.Verdict != review.CandidateVerified || result.FindingID != existing.ID || result.Promoted {
		t.Fatalf("unexpected candidate result: %+v", result)
	}
	if len(output.PromotedFindings) != 0 {
		t.Fatalf("existing finding was duplicated: %+v", output.PromotedFindings)
	}
	if pipeline.calls != 1 || len(pipeline.names) != 2 || pipeline.names[0] != analyzer.NameGoTest || pipeline.names[1] != analyzer.NameGoVet {
		t.Fatalf("focused pipeline was not invoked correctly: %+v", pipeline)
	}
}

func TestVerifierPromotesOnlyFocusedCorroborationAndCapsSeverity(t *testing.T) {
	repository, files, candidate := verificationFixture(t)
	candidate.Severity = review.SeverityHigh
	refreshCandidateIdentity(&candidate)
	focused := diagnosticFixture("FOCUSED-1")
	pipeline := &fakePipeline{output: analyzer.Output{
		Analysis: completeFocusedAnalysis(), Findings: []review.Finding{focused},
	}}

	output, err := New(pipeline).Run(context.Background(), Config{Repository: repository}, Input{
		Files: files, Agent: agentWith(candidate),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(output.PromotedFindings) != 1 || output.Verification.Summary.Promoted != 1 {
		t.Fatalf("candidate was not promoted: %+v", output)
	}
	promoted := output.PromotedFindings[0]
	if promoted.Source != "verifier:go-vet" || promoted.Severity != review.SeverityMedium || promoted.Confidence < 0.9 {
		t.Fatalf("unexpected promoted finding: %+v", promoted)
	}
	result := output.Verification.Candidates[0]
	if result.Verdict != review.CandidateVerified || !result.Promoted || result.SourceSnapshot == "" {
		t.Fatalf("unexpected candidate result: %+v", result)
	}
}

func TestVerifierKeepsUnsupportedCandidateInconclusive(t *testing.T) {
	repository, files, candidate := verificationFixture(t)
	pipeline := &fakePipeline{output: analyzer.Output{Analysis: completeFocusedAnalysis()}}
	output, err := New(pipeline).Run(context.Background(), Config{Repository: repository}, Input{
		Files: files, Agent: agentWith(candidate),
	})
	if err != nil {
		t.Fatal(err)
	}
	result := output.Verification.Candidates[0]
	if result.Verdict != review.CandidateInconclusive || result.CalibratedConfidence > 0.49 || len(output.PromotedFindings) != 0 {
		t.Fatalf("unsupported candidate escaped strict verification: %+v", output)
	}
}

func TestVerifierRejectsTamperedOrStaleCandidateBeforeTools(t *testing.T) {
	repository, files, candidate := verificationFixture(t)
	candidate.ID = "TAMPERED"
	pipeline := &fakePipeline{output: analyzer.Output{Analysis: completeFocusedAnalysis()}}
	output, err := New(pipeline).Run(context.Background(), Config{Repository: repository}, Input{
		Files: files, Agent: agentWith(candidate),
	})
	if err != nil {
		t.Fatal(err)
	}
	result := output.Verification.Candidates[0]
	if result.Verdict != review.CandidateRejected || result.Reason == "" || pipeline.calls != 0 {
		t.Fatalf("tampered candidate was not rejected early: result=%+v calls=%d", result, pipeline.calls)
	}
}

func TestVerifierMarksRunPartialWhenFocusedToolsArePartial(t *testing.T) {
	repository, files, candidate := verificationFixture(t)
	pipeline := &fakePipeline{output: analyzer.Output{Analysis: review.Analysis{
		Status: review.AnalysisPartial, CompletedStages: []string{"diff", "static-analysis-partial"},
		Tools: []review.ToolExecution{{Name: analyzer.NameGoTest, Status: review.ToolPassed}, {Name: analyzer.NameGoVet, Status: review.ToolTimedOut}},
	}}}
	output, err := New(pipeline).Run(context.Background(), Config{Repository: repository}, Input{
		Files: files, Agent: agentWith(candidate),
	})
	if err != nil {
		t.Fatal(err)
	}
	if output.Verification.Status != review.VerificationPartial || len(output.Verification.Warnings) == 0 {
		t.Fatalf("unexpected verification status: %+v", output.Verification)
	}
}

func TestMergeFindingsDeduplicatesAndSorts(t *testing.T) {
	result := MergeFindings([]review.Finding{{ID: "low", Fingerprint: "same", Severity: review.SeverityLow}}, []review.Finding{
		{ID: "duplicate", Fingerprint: "same", Severity: review.SeverityCritical},
		{ID: "high", Fingerprint: "new", Severity: review.SeverityHigh},
	})
	if len(result) != 2 || result[0].ID != "high" || result[1].ID != "low" {
		t.Fatalf("unexpected merge: %+v", result)
	}
}

func TestVerifierFocusedPipelineIntegration(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go is not installed")
	}
	repository, files, candidate := verificationFixture(t)
	if err := os.WriteFile(filepath.Join(repository, "go.mod"), []byte("module example.com/verifierfixture\n\ngo 1.23\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	output, err := New(analyzer.NewDefaultPipeline(analyzer.OSRunner{})).Run(context.Background(), Config{
		Repository: repository, AnalyzerTimeout: 30 * time.Second,
	}, Input{Files: files, Agent: agentWith(candidate)})
	if err != nil {
		t.Fatal(err)
	}
	if output.Verification.Summary.Verified != 1 || len(output.PromotedFindings) != 1 {
		t.Fatalf("focused analyzer did not corroborate the candidate: %+v", output)
	}
}

func verificationFixture(t *testing.T) (string, []review.ChangedFile, review.CandidateFinding) {
	t.Helper()
	repository := t.TempDir()
	content := "package sample\n\nimport \"fmt\"\n\nfunc Run() { fmt.Printf(\"%d\", \"bad\") }\n"
	if err := os.WriteFile(filepath.Join(repository, "main.go"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	files := []review.ChangedFile{{
		NewPath: "main.go", Status: review.FileStatusModified, Stats: review.FileStats{Additions: 1},
		Hunks: []review.Hunk{{NewStart: 5, NewLines: 1, Lines: []review.DiffLine{{
			Kind: review.LineAddition, NewLine: 5, Content: `func Run() { fmt.Printf("%d", "bad") }`,
		}}}},
	}}
	candidate := review.CandidateFinding{
		Title: "Printf format mismatch", Description: "Printf receives a string for an integer format verb.",
		Severity: review.SeverityMedium, Category: review.CategoryBug,
		Location: review.Location{Path: "main.go", StartLine: 5, EndLine: 5},
		Evidence: `fmt.Printf uses %d with the string value "bad"`, Suggestion: "Use %s or pass an integer.",
		Confidence: 0.92, Verification: []string{"Run go vet for the affected package."},
	}
	refreshCandidateIdentity(&candidate)
	return repository, files, candidate
}

func refreshCandidateIdentity(candidate *review.CandidateFinding) {
	candidate.Fingerprint = candidateFingerprint(*candidate)
	candidate.ID = "AGENT-" + strings.ToUpper(candidate.Fingerprint[:10])
}

func diagnosticFixture(id string) review.Finding {
	return review.Finding{
		ID: id, RuleID: "printf", Title: "go vet: fmt.Printf format %d has arg of wrong type string",
		Description: "Go vet reported a Printf format mismatch.", Severity: review.SeverityMedium,
		Category: review.CategoryBug, Location: review.Location{Path: "main.go", StartLine: 5},
		Evidence: "fmt.Printf format mismatch", Confidence: 0.98, Source: analyzer.NameGoVet,
		Fingerprint: "diagnostic-" + id,
	}
}

func completeFocusedAnalysis() review.Analysis {
	return review.Analysis{
		Status: review.AnalysisComplete, CompletedStages: []string{"diff", "static-analysis"},
		Tools: []review.ToolExecution{{Name: analyzer.NameGoTest, Status: review.ToolPassed}, {Name: analyzer.NameGoVet, Status: review.ToolPassed}},
	}
}

func agentWith(candidates ...review.CandidateFinding) review.AgentRun {
	agent := review.EmptyAgentRun(review.AgentComplete)
	agent.Candidates = candidates
	return agent
}
