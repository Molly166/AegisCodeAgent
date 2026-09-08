package verifier

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Molly166/AegisCodeAgent/internal/analyzer"
	"github.com/Molly166/AegisCodeAgent/internal/githubreport"
	"github.com/Molly166/AegisCodeAgent/internal/review"
)

func TestVerifierFailsClosedWhenSemanticSourceCannotBeParsed(t *testing.T) {
	repository := t.TempDir()
	if err := os.WriteFile(filepath.Join(repository, "main.go"), []byte("package sample\nfunc broken( {\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	files := []review.ChangedFile{{NewPath: "main.go", Status: review.FileStatusModified,
		Hunks: []review.Hunk{{NewStart: 2, NewLines: 1, Lines: []review.DiffLine{{Kind: review.LineAddition, NewLine: 2}}}},
	}}
	output, err := New(&fakePipeline{}).Run(context.Background(), Config{Repository: repository}, Input{Files: files, Agent: review.EmptyAgentRun(review.AgentSkipped)})
	if err != nil {
		t.Fatal(err)
	}
	if output.Verification.Status != review.VerificationPartial || len(output.Verification.Warnings) == 0 {
		t.Fatalf("parse failure did not fail closed: %+v", output.Verification)
	}
}

func TestSourceEchoCannotVerifyUnrelatedClaim(t *testing.T) {
	_, _, candidate := verificationFixture(t)
	candidate.Title = "Missing tenant permission check"
	candidate.Description = "An account can access another tenant's records."
	candidate.Evidence = diagnosticFixture("d").Evidence
	if len(findCorroborating(candidate, []review.Finding{diagnosticFixture("d")})) != 0 {
		t.Fatal("source echo verified an unrelated authorization claim")
	}
}

func TestPromotionRetainsDiagnosticClaimNotSpeculativeImpact(t *testing.T) {
	repository, files, candidate := verificationFixture(t)
	candidate.Description += " This also lets an attacker take over the operating system."
	refreshCandidateIdentity(&candidate)
	output, err := New(&fakePipeline{output: analyzer.Output{Analysis: completeFocusedAnalysis(), Findings: []review.Finding{diagnosticFixture("d")}}}).Run(context.Background(), Config{Repository: repository}, Input{Files: files, Agent: agentWith(candidate)})
	if err != nil {
		t.Fatal(err)
	}
	if len(output.PromotedFindings) != 1 {
		t.Fatalf("expected diagnostic promotion: %+v", output)
	}
	finding := output.PromotedFindings[0]
	if strings.Contains(finding.Description, "operating system") || finding.Title != diagnosticFixture("d").Title {
		t.Fatal("promoted unsupported model claim", finding)
	}
	if finding.Confidence > candidate.Confidence {
		t.Fatal("inflated correlated confidence")
	}
}

func TestModelCannotDowngradeOrConsumeIndependentP0Evidence(t *testing.T) {
	for _, scenario := range []string{"understated-model", "existing-p2-match", "higher-confidence-p2-match"} {
		t.Run(scenario, func(t *testing.T) {
			repository, files := semanticCredentialFixture(t)
			candidate := review.CandidateFinding{Title: "Credential exposed to untrusted child process", Description: "The API key is copied into a child environment.", Severity: review.SeverityMedium, Category: review.CategorySecurity, Location: review.Location{Path: "main.go", StartLine: 11, EndLine: 11}, Evidence: "DEEPSEEK_API_KEY reaches process.Env", Suggestion: "Remove the credential.", Confidence: .96}
			refreshCandidateIdentity(&candidate)
			p2 := review.Finding{ID: "EXISTING-P2", RuleID: "example-p2", Title: "Credential handling warning", Description: "A credential is copied into a child environment.", Severity: review.SeverityMedium, Category: review.CategorySecurity, Location: candidate.Location, Confidence: 1, Source: analyzer.NameGosec}
			input := Input{Files: files, Agent: agentWith(candidate)}
			pipeline := &fakePipeline{output: analyzer.Output{Analysis: completeFocusedAnalysis()}}
			switch scenario {
			case "existing-p2-match":
				input.Findings = []review.Finding{p2}
			case "higher-confidence-p2-match":
				pipeline.output.Findings = []review.Finding{p2}
			}
			output, err := New(pipeline).Run(context.Background(), Config{Repository: repository}, input)
			if err != nil {
				t.Fatal(err)
			}
			findings := MergeFindings(input.Findings, output.PromotedFindings)
			report := review.NewReport(review.Comparison{}, files, findings)
			if !githubreport.Evaluate(report, githubreport.PriorityP1, false).Blocked {
				t.Fatalf("model suppressed deterministic P0 merge blocker: %+v", findings)
			}
			critical := 0
			for _, f := range findings {
				if f.Severity == review.SeverityCritical {
					critical++
					if !strings.Contains(f.Source, "semantic:") {
						t.Fatal("critical priority transferred to an unrelated diagnostic", f)
					}
				}
			}
			if critical != 1 {
				t.Fatalf("expected exactly one independent P0, got %d: %+v", critical, findings)
			}
		})
	}
}

func TestSelectedDiagnosticOwnsItsSeverityAndConfidence(t *testing.T) {
	_, _, candidate := verificationFixture(t)
	candidate.Severity = review.SeverityCritical
	selected := diagnosticFixture("selected-p2")
	selected.Confidence = .8
	other := diagnosticFixture("other-p1")
	other.Severity = review.SeverityHigh
	other.Title = "A distinct stronger issue"
	other.Confidence = .99
	matches := []review.Finding{selected, other}
	promoted := promoteCandidate("snapshot", matches[0], calibratedConfidence(candidate.Confidence, matches))
	if promoted.Title != selected.Title || promoted.Severity != selected.Severity || promoted.Confidence > selected.Confidence {
		t.Fatalf("borrowed severity/confidence from another claim: %+v", promoted)
	}
}

func TestRephrasedCandidatesShareOneDiagnosticIdentity(t *testing.T) {
	repository, files, first := verificationFixture(t)
	second := first
	second.Title = "Printf format mismatch on the changed line"
	second.Severity = review.SeverityCritical
	refreshCandidateIdentity(&second)
	agent := agentWith(first)
	agent.Candidates = append(agent.Candidates, second)
	output, err := New(&fakePipeline{output: analyzer.Output{Analysis: completeFocusedAnalysis(), Findings: []review.Finding{diagnosticFixture("one-diagnostic")}}}).Run(context.Background(), Config{Repository: repository}, Input{Files: files, Agent: agent})
	if err != nil {
		t.Fatal(err)
	}
	if len(output.PromotedFindings) != 1 || output.Verification.Summary.Promoted != 1 || len(output.Verification.Candidates) != 2 || output.Verification.Candidates[0].FindingID != output.Verification.Candidates[1].FindingID {
		t.Fatalf("model rephrasing duplicated deterministic evidence: %+v", output)
	}
}

func TestSourceSnapshotValidatesEntireRangeEvenWhenDisplayIsTruncated(t *testing.T) {
	repository := t.TempDir()
	var err error
	repository, err = filepath.EvalSymlinks(repository)
	if err != nil {
		t.Fatal(err)
	}
	source := "package sample\n" + strings.Repeat("// "+strings.Repeat("x", 790)+"\n", 20)
	if err := os.WriteFile(filepath.Join(repository, "main.go"), []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	if snapshot, err := readSourceSnapshot(repository, review.Location{Path: "main.go", StartLine: 1, EndLine: 21}); err != nil || len(snapshot) > maxSnapshotBytes+len("…") {
		t.Fatalf("valid long range must return bounded snapshot: len=%d err=%v", len(snapshot), err)
	}
	if _, err := readSourceSnapshot(repository, review.Location{Path: "main.go", StartLine: 1, EndLine: 25}); err == nil {
		t.Fatal("verified a source range whose end does not exist")
	}
}

func TestSemanticSourceRejectsSymlinkIntoHiddenPrivateFile(t *testing.T) {
	repository := t.TempDir()
	var err error
	repository, err = filepath.EvalSymlinks(repository)
	if err != nil {
		t.Fatal(err)
	}
	private := filepath.Join(repository, ".private.go")
	if err := os.WriteFile(private, []byte("package private\nconst Private = 123\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(private, filepath.Join(repository, "leak.go")); err != nil {
		t.Skip(err)
	}
	if _, err := resolveSemanticSource(repository, "leak.go"); err == nil {
		t.Fatal("semantic verifier followed a symlink into a hidden private file")
	}
}
