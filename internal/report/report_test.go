package report

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/Molly166/AegisCodeAgent/internal/review"
)

func TestRenderMarkdown(t *testing.T) {
	reviewReport := review.NewReport(review.Comparison{
		Repository: "/tmp/example",
		Base:       "main",
		Head:       "feature",
		BaseCommit: "1234567890abcdef",
		HeadCommit: "abcdef1234567890",
	}, []review.ChangedFile{{
		NewPath: "main.go",
		Status:  review.FileStatusModified,
		Stats:   review.FileStats{Additions: 2, Deletions: 1},
	}}, []review.Finding{{
		Title:       "Unchecked error",
		Severity:    review.SeverityHigh,
		Category:    review.CategoryBug,
		Location:    review.Location{Path: "main.go", StartLine: 10},
		Confidence:  0.92,
		Source:      "staticcheck",
		Description: "The returned error is discarded.",
	}})
	reviewReport.Analysis.Tools = []review.ToolExecution{{
		Name: "go-vet", Status: review.ToolFindings, DurationMillis: 125, Findings: 1,
	}}
	reviewReport.Context = review.EmptyContextBundle(review.ContextComplete)
	reviewReport.Context.ChangedSymbols = []review.ContextSymbol{{
		ID: "example#method:*Worker.Run", QualifiedName: "*Worker.Run", Kind: review.SymbolMethod,
		Package: "example", Path: "worker.go", StartLine: 10, EndLine: 14, Signature: "func (*Worker) Run() error",
		Changed: true, RelevanceScore: 100, Reasons: []string{"changed declaration or body"},
	}}
	reviewReport.Context.Stats = review.ContextStats{PackagesLoaded: 1, FilesParsed: 2, SymbolsIndexed: 8, SymbolsSelected: 1, EstimatedTokens: 120}
	reviewReport.Agent = review.EmptyAgentRun(review.AgentComplete)
	reviewReport.Agent.Provider = "deepseek"
	reviewReport.Agent.Model = "deepseek-v4-flash"
	reviewReport.Agent.Steps = 2
	reviewReport.Agent.Summary = "One hypothesis needs verification."
	reviewReport.Agent.Candidates = []review.CandidateFinding{{
		ID: "AGENT-123", Fingerprint: "123", Title: "Possible panic", Description: "The changed path may panic.",
		Severity: review.SeverityMedium, Category: review.CategoryBug,
		Location: review.Location{Path: "main.go", StartLine: 10, EndLine: 10},
		Evidence: "panic is reachable", Suggestion: "return an error", Confidence: 0.8,
		Verification: []string{"Run a focused regression test."},
	}}
	reviewReport.Verification = review.EmptyVerificationRun(review.VerificationComplete)
	reviewReport.Verification.Summary = review.VerificationSummary{Candidates: 1, Inconclusive: 1}
	reviewReport.Verification.Tools = []review.ToolExecution{{Name: "go-test", Status: review.ToolPassed, DurationMillis: 10}}
	reviewReport.Verification.Warnings = []string{"focused vet was unavailable"}
	reviewReport.Verification.Candidates = []review.CandidateVerification{{
		CandidateID: "AGENT-123", Title: "Possible panic", Severity: review.SeverityMedium,
		Location: review.Location{Path: "main.go", StartLine: 10, EndLine: 10},
		Verdict:  review.CandidateInconclusive, Reason: "No deterministic diagnostic matched.",
		CalibratedConfidence: 0.4, SourceSnapshot: "10 | panic(value)", FindingID: "STATIC-1",
		Checks: []review.VerificationCheck{{
			Name: "changed-line", Status: review.VerificationCheckPassed, Detail: "candidate overlaps an added line",
		}}, MatchedFindingIDs: []string{},
	}}

	output := string(RenderMarkdown(reviewReport))
	for _, expected := range []string{"# AegisCodeAgent Review", "| 1 | +2 | -1 | 1 |", "Unchecked error", "main.go:10", "92%", "Repository context", "*Worker.Run", "Reasoning agent", "Unverified candidates", "Possible panic", "Verification", "inconclusive"} {
		if !strings.Contains(output, expected) {
			t.Errorf("markdown output does not contain %q:\n%s", expected, output)
		}
	}
}

func TestRenderJSONIsValid(t *testing.T) {
	reviewReport := review.NewReport(review.Comparison{Base: "main", Head: "HEAD"}, nil, nil)
	output, err := RenderJSON(reviewReport)
	if err != nil {
		t.Fatalf("RenderJSON() error = %v", err)
	}
	var decoded review.ReviewReport
	if err := json.Unmarshal(output, &decoded); err != nil {
		t.Fatalf("JSON is invalid: %v", err)
	}
	if decoded.SchemaVersion != review.SchemaVersion {
		t.Fatalf("schema version = %q, want %q", decoded.SchemaVersion, review.SchemaVersion)
	}
}

func TestRenderHTMLIsSelfContainedAndEscaped(t *testing.T) {
	reviewReport := review.NewReport(review.Comparison{
		Repository: "/tmp/<unsafe>",
		Base:       "main",
		Head:       "feature",
		BaseCommit: "1234567890abcdef",
		HeadCommit: "abcdef1234567890",
	}, []review.ChangedFile{{
		NewPath: "internal/review.go",
		Status:  review.FileStatusModified,
		Stats:   review.FileStats{Additions: 4, Deletions: 2},
	}}, []review.Finding{{
		Title:      "Potential <script>alert(1)</script>",
		Severity:   review.SeverityHigh,
		Category:   review.CategorySecurity,
		Location:   review.Location{Path: "internal/review.go", StartLine: 42},
		Confidence: 0.88,
		Evidence:   "untrusted value reaches the sink",
		Suggestion: "Validate the value before use.",
	}})
	reviewReport.Analysis.Tools = []review.ToolExecution{{
		Name: "go-vet", Status: review.ToolFindings, DurationMillis: 125, Findings: 1,
	}}
	reviewReport.Context = review.EmptyContextBundle(review.ContextComplete)
	reviewReport.Context.ChangedSymbols = []review.ContextSymbol{{
		ID: "example#method:*Worker.Run", QualifiedName: "*Worker.Run", Kind: review.SymbolMethod,
		Package: "example", Path: "worker.go", StartLine: 10, EndLine: 14,
		Signature: "func (*Worker) Run() error", Snippet: "func (w *Worker) Run() error { return nil }",
		Changed: true, RelevanceScore: 100, Reasons: []string{"changed declaration or body"},
	}}
	reviewReport.Context.Stats = review.ContextStats{PackagesLoaded: 1, FilesParsed: 2, SymbolsIndexed: 8, SymbolsSelected: 1, EstimatedTokens: 120}
	reviewReport.Agent = review.EmptyAgentRun(review.AgentComplete)
	reviewReport.Agent.Provider = "deepseek"
	reviewReport.Agent.Model = "deepseek-v4-flash"
	reviewReport.Agent.Steps = 2
	reviewReport.Agent.Usage = review.AgentUsage{PromptTokens: 100, CompletionTokens: 50, TotalTokens: 150}
	reviewReport.Agent.Candidates = []review.CandidateFinding{{
		ID: "AGENT-123", Fingerprint: "123", Title: "Potential <script>alert(2)</script>",
		Description: "The changed path may panic.", Severity: review.SeverityMedium, Category: review.CategoryBug,
		Location: review.Location{Path: "internal/review.go", StartLine: 42, EndLine: 42},
		Evidence: "panic is reachable", Suggestion: "return an error", Confidence: 0.8,
		Verification: []string{"Run a focused regression test."},
	}}
	reviewReport.Verification = review.EmptyVerificationRun(review.VerificationComplete)
	reviewReport.Verification.DurationMillis = 40
	reviewReport.Verification.Summary = review.VerificationSummary{Candidates: 1, Verified: 1, Promoted: 1}
	reviewReport.Verification.Tools = []review.ToolExecution{{Name: "go-vet", Status: review.ToolFindings, Findings: 1}}
	reviewReport.Verification.Candidates = []review.CandidateVerification{{
		CandidateID: "AGENT-123", Title: "Potential panic", Severity: review.SeverityMedium,
		Location: review.Location{Path: "internal/review.go", StartLine: 42, EndLine: 42},
		Verdict:  review.CandidateVerified, Reason: "A deterministic diagnostic matched.",
		CalibratedConfidence: 0.97, SourceSnapshot: "42 | panic(value)",
		Checks:            []review.VerificationCheck{{Name: "changed-line", Status: review.VerificationCheckPassed, Detail: "added line"}},
		MatchedFindingIDs: []string{"VET-1"}, FindingID: "AEGIS-V-123", Promoted: true,
	}}

	output, err := RenderHTML(reviewReport)
	if err != nil {
		t.Fatalf("RenderHTML() error = %v", err)
	}
	html := string(output)
	for _, expected := range []string{"<!doctype html>", "AegisCodeAgent", "risk-high", "internal/review.go", "Inspect evidence", "Analyzer execution", "go-vet", "Repository context", "*Worker.Run", "Token estimate", "Reasoning agent", "AGENT CANDIDATE", "deepseek-v4-flash", "Verification", "VERIFIED", "Exact source snapshot"} {
		if !strings.Contains(html, expected) {
			t.Errorf("HTML output does not contain %q", expected)
		}
	}
	if strings.Contains(html, "<script>alert(1)</script>") {
		t.Fatal("HTML output contains an unescaped finding title")
	}
	if strings.Contains(html, "<script>alert(2)</script>") {
		t.Fatal("HTML output contains an unescaped candidate title")
	}
	if strings.Contains(html, "https://") || strings.Contains(html, "http://") {
		t.Fatal("HTML report must not depend on remote assets")
	}
}

func TestRenderRejectsUnknownFormat(t *testing.T) {
	if _, err := Render("xml", review.ReviewReport{}); err == nil {
		t.Fatal("Render() error = nil, want unsupported format error")
	}
}

func TestScopeReportDoesNotClaimAnalysisIsClear(t *testing.T) {
	reviewReport := review.NewScopeReport(review.Comparison{}, nil)

	html, err := RenderHTML(reviewReport)
	if err != nil {
		t.Fatalf("RenderHTML() error = %v", err)
	}
	if !strings.Contains(string(html), "Scope mapped") || !strings.Contains(string(html), "analysis pending") {
		t.Fatalf("HTML does not identify scope-only analysis:\n%s", html)
	}
	markdown := string(RenderMarkdown(reviewReport))
	if !strings.Contains(markdown, "analysis has not run") {
		t.Fatalf("Markdown does not identify scope-only analysis:\n%s", markdown)
	}
}

func TestUnverifiedAgentCandidateDoesNotChangeVerdict(t *testing.T) {
	reviewReport := review.NewReport(review.Comparison{}, nil, nil)
	reviewReport.Agent = review.EmptyAgentRun(review.AgentComplete)
	reviewReport.Agent.Candidates = []review.CandidateFinding{{
		Title: "Unverified critical hypothesis", Severity: review.SeverityCritical,
	}}
	if got := riskClass(reviewReport); got != "clear" {
		t.Fatalf("riskClass() = %q, want clear for an unverified candidate", got)
	}
	if got := riskLabel(reviewReport); got != "No findings" {
		t.Fatalf("riskLabel() = %q, want No findings for an unverified candidate", got)
	}
}

func TestVerificationFailureAndPartialAreVisibleInVerdict(t *testing.T) {
	failed := review.NewReport(review.Comparison{}, nil, []review.Finding{{Severity: review.SeverityHigh}})
	failed.Verification = review.EmptyVerificationRun(review.VerificationFailed)
	if got := riskClass(failed); got != "critical" {
		t.Fatalf("failed verification riskClass() = %q, want critical", got)
	}
	if got := riskLabel(failed); got != "Verification failed" {
		t.Fatalf("failed verification riskLabel() = %q, want Verification failed", got)
	}

	partial := review.NewReport(review.Comparison{}, nil, []review.Finding{{Severity: review.SeverityLow}})
	partial.Verification = review.EmptyVerificationRun(review.VerificationPartial)
	if got := riskClass(partial); got != "medium" {
		t.Fatalf("partial verification riskClass() = %q, want medium", got)
	}
	if got := riskLabel(partial); got != "Partial verification" {
		t.Fatalf("partial verification riskLabel() = %q, want Partial verification", got)
	}
}

func TestNeedsReviewCannotRenderAsNoFindings(t *testing.T) {
	reviewReport := review.NewReport(review.Comparison{}, nil, nil)
	reviewReport.Verification = review.EmptyVerificationRun(review.VerificationComplete)
	reviewReport.Verification.Summary = review.VerificationSummary{Candidates: 1, NeedsReview: 1}
	reviewReport.Verification.Candidates = []review.CandidateVerification{{
		Title: "Possible authorization bypass", Severity: review.SeverityCritical,
		Location: review.Location{Path: "auth.go", StartLine: 20}, Verdict: review.CandidateNeedsReview,
	}}
	if got := riskClass(reviewReport); got != "critical" {
		t.Fatalf("riskClass() = %q, want critical", got)
	}
	if got := riskLabel(reviewReport); got != "Needs review" {
		t.Fatalf("riskLabel() = %q, want Needs review", got)
	}
	html, err := RenderHTML(reviewReport)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(html), "NEEDS REVIEW") || strings.Contains(string(html), ">No findings<") {
		t.Fatalf("needs-review HTML was presented as clean")
	}
}

func TestIsSupported(t *testing.T) {
	for _, format := range []string{"html", "markdown", "md", "json", "HTML"} {
		if !IsSupported(format) {
			t.Errorf("IsSupported(%q) = false, want true", format)
		}
	}
	if IsSupported("xml") {
		t.Fatal("IsSupported(\"xml\") = true, want false")
	}
}
