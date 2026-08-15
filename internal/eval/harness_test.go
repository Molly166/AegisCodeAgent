package eval

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Molly166/AegisCodeAgent/internal/githubreport"
	"github.com/Molly166/AegisCodeAgent/internal/review"
)

func TestHarnessMeasuresFindingsAndGate(t *testing.T) {
	corpus := t.TempDir()
	critical := review.NewReport(review.Comparison{Base: "a", Head: "b"}, []review.ChangedFile{{NewPath: "main.go"}}, []review.Finding{{
		ID: "SEC-1", RuleID: "AEGIS-SEC-001", Title: "Sensitive credential propagated", Severity: review.SeverityCritical,
		Category: review.CategorySecurity, Location: review.Location{Path: "main.go", StartLine: 11}, Source: "semantic:credential-flow",
	}})
	completeReport(&critical)
	clean := review.NewReport(review.Comparison{Base: "a", Head: "c"}, []review.ChangedFile{{NewPath: "README.md"}}, nil)
	completeReport(&clean)
	writeCase(t, corpus, "SEC-001", critical, CaseSpec{
		SchemaVersion: CaseSchemaVersion, ID: "SEC-001", Title: "Credential flow", Report: "report.json",
		Description: "Credential reaches an untrusted child process.", Kind: CaseKindBug, Layer: LayerSemantic,
		Provenance: ProvenanceRegression, Tags: []string{"security", "p0"}, ExpectedGate: GateBlocked,
		ExpectedFindings: []ExpectedFinding{{
			Severity: review.SeverityCritical, Category: review.CategorySecurity, Path: "main.go", StartLine: 11,
			RuleID: "AEGIS-SEC-001", TitleContains: "credential",
		}},
	})
	writeCase(t, corpus, "CLEAN-001", clean, CaseSpec{
		SchemaVersion: CaseSchemaVersion, ID: "CLEAN-001", Title: "Documentation only", Report: "report.json",
		Description: "Documentation change contains no executable risk.", Kind: CaseKindClean, Layer: LayerContext,
		Provenance: ProvenanceCuratedSynthetic, Tags: []string{"clean"}, ExpectedGate: GatePassed, ExpectedFindings: []ExpectedFinding{},
	})

	result, err := Run(Config{Corpus: corpus, Gate: githubreport.Options{
		FailOn: githubreport.PriorityP1, FailOnNeedsReview: githubreport.PriorityP0, FailOnIncomplete: true,
	}})
	if err != nil {
		t.Fatal(err)
	}
	if result.Metrics.Cases != 2 || result.Metrics.PassedCases != 2 || result.Metrics.Precision != 1 || result.Metrics.Recall != 1 || result.Metrics.GateAccuracy != 1 {
		t.Fatalf("unexpected eval metrics: %+v", result.Metrics)
	}
	if result.Metrics.P0Expected != 1 || result.Metrics.P0Matched != 1 || result.Metrics.FalseBlockRate != 0 || result.Metrics.BugCases != 1 || result.Metrics.CleanCases != 1 {
		t.Fatalf("priority or clean-case metrics are incorrect: %+v", result.Metrics)
	}
	html, err := Render(FormatHTML, result)
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"Review quality, measured", "100.0%", "SEC-001", "CLEAN-001"} {
		if !strings.Contains(string(html), expected) {
			t.Errorf("HTML report does not contain %q", expected)
		}
	}
}

func TestNeedsReviewBlocksButDoesNotCountAsRecall(t *testing.T) {
	report := review.NewReport(review.Comparison{}, []review.ChangedFile{{NewPath: "main.go"}}, nil)
	completeReport(&report)
	report.Verification.Summary = review.VerificationSummary{Candidates: 1, NeedsReview: 1}
	report.Verification.Candidates = []review.CandidateVerification{{
		Title: "Possible credential leak", Severity: review.SeverityCritical,
		Location: review.Location{Path: "main.go", StartLine: 5}, Verdict: review.CandidateNeedsReview,
	}}
	result := evaluateCase(CaseSpec{
		ID: "SEC-MISS", Title: "Missed semantic issue", ExpectedGate: GateBlocked,
		ExpectedFindings: []ExpectedFinding{{Severity: review.SeverityCritical, Path: "main.go", StartLine: 5}},
	}, report, "report.json", githubreport.Options{FailOn: githubreport.PriorityP1, FailOnNeedsReview: githubreport.PriorityP0})
	metrics := calculateMetrics([]CaseResult{result})
	if result.Passed || !result.GateCorrect || len(result.Missed) != 1 || result.NeedsReview != 1 {
		t.Fatalf("unresolved hypothesis was incorrectly credited: %+v", result)
	}
	if metrics.P0Recall != 0 || metrics.UnresolvedHypotheses != 1 {
		t.Fatalf("unresolved hypothesis distorted recall: %+v", metrics)
	}
}

func TestUnexpectedNeedsReviewFailsCleanCaseEvenWhenGateIsRelaxed(t *testing.T) {
	report := review.NewReport(review.Comparison{}, nil, nil)
	completeReport(&report)
	report.Verification.Candidates = []review.CandidateVerification{{
		Title: "Unresolved behavior", Severity: review.SeverityMedium,
		Verdict: review.CandidateNeedsReview,
	}}
	result := evaluateCase(CaseSpec{
		ID: "CLEAN-UNRESOLVED", Title: "Clean case", ExpectedGate: GatePassed,
	}, report, "report.json", githubreport.Options{FailOn: githubreport.PriorityP1, FailOnNeedsReview: githubreport.PriorityNone})
	if result.Passed || !result.GateCorrect || result.NeedsReview != 1 {
		t.Fatalf("unexpected unresolved hypothesis was treated as clean: %+v", result)
	}
}

func TestCheckedInCorpusHasRequiredDistribution(t *testing.T) {
	corpus := filepath.Join("..", "..", "eval", "cases")
	result, err := Run(Config{Corpus: corpus, Gate: githubreport.Options{
		FailOn: githubreport.PriorityP1, FailOnNeedsReview: githubreport.PriorityP0, FailOnIncomplete: true,
	}})
	if err != nil {
		t.Fatal(err)
	}
	if result.SchemaVersion != ReportSchemaVersion || result.Mode != ModeGoldenReplay {
		t.Fatalf("checked-in corpus report identity changed: schema=%q mode=%q", result.SchemaVersion, result.Mode)
	}
	metrics := result.Metrics
	if metrics.Cases != 50 || metrics.PassedCases != 50 || metrics.BugCases != 27 || metrics.CleanCases != 15 || metrics.NeedsReviewCases != 5 || metrics.ResilienceCases != 3 {
		t.Fatalf("checked-in corpus composition changed unexpectedly: %+v", metrics)
	}
	if metrics.P0Expected != 9 || metrics.P1Expected != 9 || metrics.P2Expected != 6 || metrics.P3Expected != 3 {
		t.Fatalf("checked-in priority distribution changed unexpectedly: %+v", metrics)
	}
	if metrics.ExpectedFindings != 27 || metrics.UnresolvedHypotheses != 5 || metrics.GateCorrect != 50 || metrics.FalseBlocks != 0 {
		t.Fatalf("checked-in corpus quality contract failed: %+v", metrics)
	}

	layers := make(map[EvaluationLayer]int)
	provenance := make(map[CaseProvenance]int)
	blocked := 0
	for _, item := range result.Cases {
		layers[item.Layer]++
		provenance[item.Provenance]++
		if item.ExpectedGate == GateBlocked {
			blocked++
		}
	}
	for _, layer := range []EvaluationLayer{LayerStatic, LayerSemantic, LayerAgent, LayerGate, LayerContext, LayerMixed} {
		if layers[layer] == 0 {
			t.Errorf("checked-in corpus does not exercise %q", layer)
		}
	}
	if provenance[ProvenanceRegression] != 4 || provenance[ProvenanceCuratedSynthetic] != 46 || blocked != 22 {
		t.Fatalf("checked-in provenance or gate distribution changed: provenance=%v blocked=%d", provenance, blocked)
	}
}

func completeReport(report *review.ReviewReport) {
	report.Context = review.EmptyContextBundle(review.ContextComplete)
	report.Agent = review.EmptyAgentRun(review.AgentSkipped)
	report.Verification = review.EmptyVerificationRun(review.VerificationComplete)
	report.RecalculateSummary()
}

func writeCase(t *testing.T, corpus, directory string, report review.ReviewReport, spec CaseSpec) {
	t.Helper()
	path := filepath.Join(corpus, directory)
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
	writeJSON(t, filepath.Join(path, "case.json"), spec)
	writeJSON(t, filepath.Join(path, "report.json"), report)
}

func writeJSON(t *testing.T, path string, value any) {
	t.Helper()
	encoded, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, encoded, 0o600); err != nil {
		t.Fatal(err)
	}
}
