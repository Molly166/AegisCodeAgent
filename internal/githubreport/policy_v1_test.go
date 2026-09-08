package githubreport

import (
	"strings"
	"testing"

	"github.com/Molly166/AegisCodeAgent/internal/review"
)

func TestIncompleteWaiverNeverMeansClean(t *testing.T) {
	r := review.NewReport(review.Comparison{}, nil, nil)
	r.Analysis.Status = review.AnalysisPartial
	summary := string(RenderSummary(r, Options{FailOn: PriorityP1, FailOnIncomplete: false}))
	if strings.Contains(summary, "completed without findings") || !strings.Contains(summary, "Review incomplete") {
		t.Fatal(summary)
	}
}

func TestRequireAgentIsExplicitAndIndependentOfIncompleteWaiver(t *testing.T) {
	r := review.NewReport(review.Comparison{}, []review.ChangedFile{{NewPath: "main.go"}}, nil)
	for _, status := range []review.AgentStatus{review.AgentNotRun, review.AgentSkipped, review.AgentPartial, review.AgentFailed} {
		r.Agent.Status = status
		gate := EvaluateWithOptions(r, Options{FailOn: PriorityP1, RequireAgent: true})
		if !gate.Blocked || !gate.BlockedByRequiredAgent {
			t.Fatalf("%s: %+v", status, gate)
		}
	}
	r.Agent.Status = review.AgentComplete
	if EvaluateWithOptions(r, Options{RequireAgent: true}).Blocked {
		t.Fatal("complete agent blocked")
	}
	r.Agent.Status = review.AgentSkipped
	r.Files[0].NewPath = "README.md"
	if EvaluateWithOptions(r, Options{RequireAgent: true}).Blocked {
		t.Fatal("docs-only skipped agent blocked")
	}
}

func TestUnverifiedP0RemainsVisibleWhenVerifierDisabled(t *testing.T) {
	r := review.NewReport(review.Comparison{}, nil, nil)
	r.Agent.Status = review.AgentComplete
	r.Agent.Candidates = []review.CandidateFinding{{ID: "candidate", Title: "credential leak", Severity: review.SeverityCritical}}
	gate := EvaluateWithOptions(r, Options{FailOn: PriorityP1, FailOnNeedsReview: PriorityP0})
	if !gate.BlockedByNeedsReview || !gate.NeedsReview {
		t.Fatalf("orphan hypothesis disappeared: %+v", gate)
	}
	summary := string(RenderSummary(r, Options{}))
	if !strings.Contains(summary, "credential leak") || strings.Contains(summary, "completed without findings") {
		t.Fatal(summary)
	}
	if len(r.Verification.Candidates) != 0 {
		t.Fatal("gate mutated source report")
	}
}

func TestRequiredAgentMatchesReasoningSourceScope(t *testing.T) {
	for _, path := range []string{"main.go", "go.mod", "api.proto", "schema.sql", ".github/workflows/ci.yml", "config.JSON"} {
		r := review.NewReport(review.Comparison{}, []review.ChangedFile{{NewPath: path}}, nil)
		r.Agent.Status = review.AgentSkipped
		if !EvaluateWithOptions(r, Options{RequireAgent: true}).BlockedByRequiredAgent {
			t.Errorf("skipped supported source was accepted: %s", path)
		}
	}
	for _, file := range []review.ChangedFile{{NewPath: "README.md"}, {NewPath: ".private/config.json"}, {NewPath: "main.go", Binary: true}, {OldPath: "main.go", Status: review.FileStatusDeleted}} {
		r := review.NewReport(review.Comparison{}, []review.ChangedFile{file}, nil)
		r.Agent.Status = review.AgentSkipped
		if EvaluateWithOptions(r, Options{RequireAgent: true}).BlockedByRequiredAgent {
			t.Errorf("unsupported source required agent: %+v", file)
		}
	}
}

func TestUnknownVerificationCannotAppearClean(t *testing.T) {
	r := review.NewReport(review.Comparison{}, nil, nil)
	r.Verification.Status = "typo"
	if !EvaluateWithOptions(r, Options{FailOnIncomplete: true}).Blocked {
		t.Fatal("unknown verification status passed")
	}
	r.Verification.Status = review.VerificationComplete
	r.Verification.Candidates = []review.CandidateVerification{{CandidateID: "p0", Severity: review.SeverityCritical, Verdict: "typo"}}
	gate := EvaluateWithOptions(r, Options{})
	if !gate.BlockedByNeedsReview || !gate.Incomplete {
		t.Fatalf("unknown verdict hid a P0 hypothesis: %+v", gate)
	}
	if r.Verification.Candidates[0].Verdict != "typo" {
		t.Fatal("source evidence was mutated")
	}
}
