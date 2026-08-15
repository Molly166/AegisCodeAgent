package githubreport

import (
	"strings"
	"testing"

	"github.com/Molly166/AegisCodeAgent/internal/review"
)

func TestPriorityMappingAndGate(t *testing.T) {
	report := reportFixture()
	counts := Count(report)
	if counts.P0 != 1 || counts.P1 != 1 || counts.P2 != 1 || counts.P3 != 1 {
		t.Fatalf("unexpected counts: %+v", counts)
	}
	if result := Evaluate(report, PriorityP1, true); !result.Blocked || result.Highest != PriorityP0 || result.Incomplete {
		t.Fatalf("unexpected gate result: %+v", result)
	}
	if result := Evaluate(report, PriorityNone, true); result.Blocked {
		t.Fatalf("none threshold blocked complete report: %+v", result)
	}
	for input, expected := range map[string]Priority{"P0": PriorityP0, "p1": PriorityP1, " p2 ": PriorityP2, "P3": PriorityP3, "none": PriorityNone} {
		actual, err := ParsePriority(input)
		if err != nil || actual != expected {
			t.Fatalf("ParsePriority(%q) = %q, %v", input, actual, err)
		}
	}
	if _, err := ParsePriority("critical"); err == nil {
		t.Fatal("invalid priority was accepted")
	}
}

func TestIncompleteReviewBlocksIndependently(t *testing.T) {
	report := review.NewScopeReport(review.Comparison{}, nil)
	if result := Evaluate(report, PriorityNone, true); !result.Blocked || !result.Incomplete || len(result.IncompleteReasons) != 1 {
		t.Fatalf("scope-only report should be incomplete: %+v", result)
	}
	report.Analysis.Status = review.AnalysisPartial
	report.Context.Status = review.ContextFailed
	report.Agent.Status = review.AgentPartial
	report.Verification.Status = review.VerificationFailed
	result := Evaluate(report, PriorityNone, true)
	if !result.Blocked || !result.Incomplete || len(result.IncompleteReasons) != 2 || !result.Degraded || len(result.DegradedReasons) != 2 {
		t.Fatalf("unexpected incomplete result: %+v", result)
	}
	if Evaluate(report, PriorityNone, false).Blocked {
		t.Fatal("incomplete report blocked when fail-on-incomplete was disabled")
	}
}

func TestP2AndAgentDegradationDoNotBlockP1Gate(t *testing.T) {
	report := review.NewReport(review.Comparison{}, []review.ChangedFile{{NewPath: "main.go"}}, []review.Finding{{
		Title: "Potential file inclusion", Severity: review.SeverityMedium,
		Location: review.Location{Path: "main.go", StartLine: 10}, Source: "gosec",
	}})
	report.Context = review.EmptyContextBundle(review.ContextComplete)
	report.Agent = review.EmptyAgentRun(review.AgentPartial)
	report.Verification = review.EmptyVerificationRun(review.VerificationPartial)
	report.Verification.Warnings = []string{"reasoning agent was partial"}

	result := EvaluateWithOptions(report, Options{
		FailOn: PriorityP1, FailOnNeedsReview: PriorityP0, FailOnIncomplete: true,
	})
	if result.Blocked || result.Incomplete || !result.Degraded || result.Highest != PriorityP2 {
		t.Fatalf("P2 finding or optional Agent degradation blocked the P1 gate: %+v", result)
	}
	summary := string(RenderSummary(report, Options{
		FailOn: PriorityP1, FailOnNeedsReview: PriorityP0, FailOnIncomplete: true,
	}))
	for _, expected := range []string{"degraded optional stages", "P2", "P2/P3 findings remain visible without blocking"} {
		if !strings.Contains(summary, expected) {
			t.Errorf("degraded summary does not contain %q:\n%s", expected, summary)
		}
	}

	report.Verification.Warnings = append(report.Verification.Warnings, "semantic source parsing failed")
	unsafe := EvaluateWithOptions(report, Options{
		FailOn: PriorityP1, FailOnNeedsReview: PriorityP0, FailOnIncomplete: true,
	})
	if !unsafe.Blocked || !unsafe.Incomplete {
		t.Fatalf("real Verifier incompleteness was waived with Agent degradation: %+v", unsafe)
	}
}

func TestSkippedOptionalStagesDoNotBlock(t *testing.T) {
	report := review.NewReport(review.Comparison{}, nil, nil)
	report.Agent = review.EmptyAgentRun(review.AgentSkipped)
	result := Evaluate(report, PriorityP1, true)
	if result.Blocked || result.Incomplete {
		t.Fatalf("skipped optional reasoning stage blocked the review: %+v", result)
	}
}

func TestNeedsReviewIsVisibleAndCanBlock(t *testing.T) {
	report := review.NewReport(review.Comparison{Base: "master", Head: "feature"}, []review.ChangedFile{{NewPath: "main.go"}}, nil)
	report.Context = review.EmptyContextBundle(review.ContextComplete)
	report.Agent = review.EmptyAgentRun(review.AgentComplete)
	report.Verification = review.EmptyVerificationRun(review.VerificationComplete)
	report.Verification.Summary = review.VerificationSummary{Candidates: 1, NeedsReview: 1}
	report.Verification.Candidates = []review.CandidateVerification{{
		CandidateID: "AGENT-1", Title: "Possible credential leak", Severity: review.SeverityCritical,
		Location: review.Location{Path: "main.go", StartLine: 10}, Verdict: review.CandidateNeedsReview,
		Reason: "semantic evidence is unresolved",
	}}

	gate := EvaluateWithOptions(report, Options{FailOn: PriorityP1, FailOnNeedsReview: PriorityP0, FailOnIncomplete: true})
	if !gate.Blocked || !gate.BlockedByNeedsReview || gate.NeedsReviewHighest != PriorityP0 {
		t.Fatalf("P0 needs-review hypothesis did not block: %+v", gate)
	}
	if relaxed := EvaluateWithOptions(report, Options{FailOn: PriorityP1, FailOnNeedsReview: PriorityNone}); relaxed.Blocked || !relaxed.NeedsReview {
		t.Fatalf("disabled needs-review gate hid or blocked the hypothesis: %+v", relaxed)
	}
	summary := string(RenderSummary(report, Options{FailOn: PriorityP1, FailOnNeedsReview: PriorityP0}))
	for _, expected := range []string{"blocked pending human review", "Needs human review", "Possible credential leak", "P0", "1 needs review"} {
		if !strings.Contains(summary, expected) {
			t.Errorf("needs-review summary does not contain %q:\n%s", expected, summary)
		}
	}
	if strings.Contains(summary, "completed without findings") {
		t.Fatalf("unresolved review was presented as clean:\n%s", summary)
	}
	annotations := string(RenderAnnotations(report, 10))
	if !strings.Contains(annotations, "NEEDS REVIEW") || !strings.Contains(annotations, "file=main.go") {
		t.Fatalf("needs-review annotation missing: %s", annotations)
	}
}

func TestRenderSummary(t *testing.T) {
	report := reportFixture()
	output := string(RenderSummary(report, Options{
		FailOn: PriorityP1, FailOnIncomplete: true, ArtifactName: "aegis-review-report", MaxFindings: 2,
	}))
	for _, expected := range []string{"Aegis Code Review", "Merge gate blocked", "| 1 | 1 | 1 | 1 |", "P0", "main.go:10", "aegis-review-report", "additional finding"} {
		if !strings.Contains(output, expected) {
			t.Errorf("summary does not contain %q:\n%s", expected, output)
		}
	}
}

func TestRenderAnnotationsEscapesCommandsAndBoundsOutput(t *testing.T) {
	report := reportFixture()
	report.Findings[0].Title = "unsafe,title: 100%\n::warning::inject"
	report.Findings[0].Description = "first line\n::error::injected"
	report.Findings[0].Location.Path = "internal/a,b.go"
	output := string(RenderAnnotations(report, 2))
	for _, expected := range []string{"::error ", "title=P0 · unsafe%2Ctitle%3A 100%25", "file=internal/a%2Cb.go", "first line ::error::injected", "additional finding(s)"} {
		if !strings.Contains(output, expected) {
			t.Errorf("annotations do not contain %q:\n%s", expected, output)
		}
	}
	if strings.Count(output, "\n") != 3 {
		t.Fatalf("unexpected annotation line count: %q", output)
	}
}

func TestUnsafeAnnotationPathIsOmitted(t *testing.T) {
	report := reportFixture()
	report.Findings = report.Findings[:1]
	report.Findings[0].Location.Path = "../outside.go"
	output := string(RenderAnnotations(report, 10))
	if strings.Contains(output, "file=") || !strings.Contains(output, "title=P0") {
		t.Fatalf("unsafe path was published: %s", output)
	}
}

func reportFixture() review.ReviewReport {
	report := review.NewReport(review.Comparison{Base: "master", Head: "feature"}, []review.ChangedFile{{NewPath: "main.go"}}, []review.Finding{
		{Title: "Critical corruption", Severity: review.SeverityCritical, Location: review.Location{Path: "main.go", StartLine: 10}, Description: "data can be corrupted", Source: "gosec"},
		{Title: "Nil dereference", Severity: review.SeverityHigh, Location: review.Location{Path: "main.go", StartLine: 20}, Evidence: "nil reaches dereference", Source: "verifier:go-test"},
		{Title: "Performance regression", Severity: review.SeverityMedium, Location: review.Location{Path: "main.go", StartLine: 30}, Source: "agent"},
		{Title: "Maintenance issue", Severity: review.SeverityLow, Location: review.Location{Path: "main.go", StartLine: 40}, Suggestion: "simplify it", Source: "staticcheck"},
	})
	report.Context = review.EmptyContextBundle(review.ContextComplete)
	return report
}
