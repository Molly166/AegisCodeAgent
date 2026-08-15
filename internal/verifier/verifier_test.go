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

func TestVerifierKeepsUnsupportedCandidateForHumanReview(t *testing.T) {
	repository, files, candidate := verificationFixture(t)
	pipeline := &fakePipeline{output: analyzer.Output{Analysis: completeFocusedAnalysis()}}
	output, err := New(pipeline).Run(context.Background(), Config{Repository: repository}, Input{
		Files: files, Agent: agentWith(candidate),
	})
	if err != nil {
		t.Fatal(err)
	}
	result := output.Verification.Candidates[0]
	if result.Verdict != review.CandidateNeedsReview || result.CalibratedConfidence > 0.69 || output.Verification.Summary.NeedsReview != 1 || len(output.PromotedFindings) != 0 {
		t.Fatalf("unsupported candidate escaped strict verification: %+v", output)
	}
}

func TestVerifierStatusDoesNotInheritAgentDegradation(t *testing.T) {
	repository := t.TempDir()
	agentRun := review.EmptyAgentRun(review.AgentPartial)
	agentRun.Warnings = []string{"agent stopped after reaching the configured step limit"}

	output, err := New(&fakePipeline{}).Run(context.Background(), Config{Repository: repository}, Input{Agent: agentRun})
	if err != nil {
		t.Fatal(err)
	}
	if output.Verification.Status != review.VerificationComplete || len(output.Verification.Warnings) != 0 {
		t.Fatalf("verifier inherited an unrelated Agent degradation: %+v", output.Verification)
	}
}

func TestVerifierSemanticEvidenceBlocksCredentialLeakWithoutAgentCandidate(t *testing.T) {
	repository, files := semanticCredentialFixture(t)
	output, err := New(&fakePipeline{}).Run(context.Background(), Config{Repository: repository}, Input{
		Files: files, Agent: review.EmptyAgentRun(review.AgentSkipped),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(output.PromotedFindings) != 1 || output.PromotedFindings[0].Severity != review.SeverityCritical {
		t.Fatalf("semantic credential leak was not promoted as P0: %+v", output)
	}
	if output.Verification.Summary.SemanticFindings != 1 || output.Verification.Summary.Promoted != 1 {
		t.Fatalf("semantic verification summary is incomplete: %+v", output.Verification.Summary)
	}
}

func TestVerifierCorroboratesAgentCandidateWithSemanticEvidence(t *testing.T) {
	repository, files := semanticCredentialFixture(t)
	candidate := review.CandidateFinding{
		Title: "Credential exposed to untrusted child process", Description: "The API key is copied into a child environment.",
		Severity: review.SeverityCritical, Category: review.CategorySecurity,
		Location: review.Location{Path: "main.go", StartLine: 11, EndLine: 11},
		Evidence: "DEEPSEEK_API_KEY reaches process.Env", Suggestion: "Remove the credential.", Confidence: 0.96,
	}
	refreshCandidateIdentity(&candidate)
	pipeline := &fakePipeline{output: analyzer.Output{Analysis: completeFocusedAnalysis()}}
	output, err := New(pipeline).Run(context.Background(), Config{Repository: repository}, Input{Files: files, Agent: agentWith(candidate)})
	if err != nil {
		t.Fatal(err)
	}
	if output.Verification.Summary.Verified != 1 || output.Verification.Summary.NeedsReview != 0 || len(output.PromotedFindings) != 1 {
		t.Fatalf("semantic evidence did not verify candidate: %+v", output)
	}
	if finding := output.PromotedFindings[0]; finding.Severity != review.SeverityCritical || finding.Source != "verifier:semantic:credential-flow" {
		t.Fatalf("unexpected semantic promotion: %+v", finding)
	}
}

func TestVerifierSemanticRuleDoesNotFlagArbitraryEnvField(t *testing.T) {
	repository := t.TempDir()
	content := `package sample

import "os"

type Config struct { Env []string }
func Run(config *Config) {
	config.Env = append(config.Env, "SERVICE_TOKEN="+os.Getenv("SERVICE_TOKEN"))
}

func TestVerifierFailsClosedWhenSemanticSourceCannotBeParsed(t *testing.T) {
	repository := t.TempDir()
	if err := os.WriteFile(filepath.Join(repository, "main.go"), []byte("package sample\nfunc broken( {\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	files := []review.ChangedFile{{
		NewPath: "main.go", Status: review.FileStatusModified,
		Hunks: []review.Hunk{{NewStart: 2, NewLines: 1, Lines: []review.DiffLine{{Kind: review.LineAddition, NewLine: 2}}}},
	}}
	output, err := New(&fakePipeline{}).Run(context.Background(), Config{Repository: repository}, Input{
		Files: files, Agent: review.EmptyAgentRun(review.AgentSkipped),
	})
	if err != nil {
		t.Fatal(err)
	}
	if output.Verification.Status != review.VerificationPartial || len(output.Verification.Warnings) == 0 {
		t.Fatalf("semantic parse failure did not fail closed: %+v", output.Verification)
	}
}
`
	if err := os.WriteFile(filepath.Join(repository, "main.go"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	files := []review.ChangedFile{{
		NewPath: "main.go", Status: review.FileStatusModified,
		Hunks: []review.Hunk{{NewStart: 7, NewLines: 1, Lines: []review.DiffLine{{Kind: review.LineAddition, NewLine: 7}}}},
	}}
	output, err := New(&fakePipeline{}).Run(context.Background(), Config{Repository: repository}, Input{
		Files: files, Agent: review.EmptyAgentRun(review.AgentSkipped),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(output.PromotedFindings) != 0 || output.Verification.Summary.SemanticFindings != 0 {
		t.Fatalf("arbitrary Env field was treated as a child-process sink: %+v", output)
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

func semanticCredentialFixture(t *testing.T) (string, []review.ChangedFile) {
	t.Helper()
	repository := t.TempDir()
	content := `package sample

import (
	"os"
	"os/exec"
)

func ForUntrustedChild(environment []string) []string { return environment }
func Run() {
	process := exec.Command("helper")
	process.Env = append(ForUntrustedChild(os.Environ()), "AEGIS_REVIEW_TOKEN="+os.Getenv("DEEPSEEK_API_KEY"))
}
`
	if err := os.WriteFile(filepath.Join(repository, "main.go"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return repository, []review.ChangedFile{{
		NewPath: "main.go", Status: review.FileStatusModified, Stats: review.FileStats{Additions: 1},
		Hunks: []review.Hunk{{NewStart: 11, NewLines: 1, Lines: []review.DiffLine{{
			Kind: review.LineAddition, NewLine: 11,
			Content: `process.Env = append(ForUntrustedChild(os.Environ()), "AEGIS_REVIEW_TOKEN="+os.Getenv("DEEPSEEK_API_KEY"))`,
		}}}},
	}}
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
