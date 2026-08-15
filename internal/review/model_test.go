package review

import "testing"

func TestNewReportCalculatesSummary(t *testing.T) {
	report := NewReport(Comparison{}, []ChangedFile{
		{Stats: FileStats{Additions: 3, Deletions: 1}},
		{Stats: FileStats{Additions: 2, Deletions: 4}},
	}, []Finding{
		{Severity: SeverityCritical},
		{Severity: SeverityHigh},
		{Severity: SeverityHigh},
		{Severity: SeverityInfo},
	})

	if report.SchemaVersion != SchemaVersion {
		t.Fatalf("schema version = %q, want %q", report.SchemaVersion, SchemaVersion)
	}
	if report.Summary.ChangedFiles != 2 || report.Summary.Additions != 5 || report.Summary.Deletions != 5 {
		t.Fatalf("unexpected change summary: %+v", report.Summary)
	}
	if report.Summary.Findings != 4 || report.Summary.Critical != 1 || report.Summary.High != 2 || report.Summary.Info != 1 {
		t.Fatalf("unexpected finding summary: %+v", report.Summary)
	}
}

func TestNewReportUsesEmptySlices(t *testing.T) {
	report := NewReport(Comparison{}, nil, nil)
	if report.Files == nil || report.Findings == nil || report.Analysis.CompletedStages == nil || report.Analysis.Tools == nil || report.Context.Intent.Labels == nil || report.Context.Intent.LinkedIssues == nil || report.Context.Intent.RepositoryGuidance == nil || report.Context.ChangedSymbols == nil || report.Context.RelatedSymbols == nil || report.Context.Relations == nil || report.Context.Warnings == nil || report.Agent.Candidates == nil || report.Agent.ToolCalls == nil || report.Agent.Warnings == nil || report.Verification.Tools == nil || report.Verification.Candidates == nil || report.Verification.Warnings == nil {
		t.Fatalf("report slices must be non-nil: %+v", report)
	}
}

func TestUpgradeReportMigratesV5InconclusiveToNeedsReview(t *testing.T) {
	report := ReviewReport{
		SchemaVersion: PreviousSchemaVersion,
		Verification: VerificationRun{
			Summary:    VerificationSummary{Candidates: 1, Inconclusive: 1},
			Candidates: []CandidateVerification{{Verdict: CandidateInconclusive, Severity: SeverityCritical}},
		},
	}
	if err := UpgradeReport(&report); err != nil {
		t.Fatal(err)
	}
	if report.SchemaVersion != SchemaVersion || report.Verification.Summary.NeedsReview != 1 || report.Verification.Summary.Inconclusive != 0 || report.Verification.Candidates[0].Verdict != CandidateNeedsReview {
		t.Fatalf("v5 report was not safely migrated: %+v", report)
	}
	if report.Context.Intent.Labels == nil || report.Context.Intent.LinkedIssues == nil || report.Context.Intent.RepositoryGuidance == nil {
		t.Fatalf("migrated intent slices must be non-nil: %+v", report.Context.Intent)
	}
	if err := UpgradeReport(&ReviewReport{SchemaVersion: "v4"}); err == nil {
		t.Fatal("unsupported schema was accepted")
	}
}

func TestNewScopeReportMarksAnalysisPending(t *testing.T) {
	report := NewScopeReport(Comparison{}, nil)
	if report.Analysis.Status != AnalysisScopeOnly {
		t.Fatalf("analysis status = %q, want %q", report.Analysis.Status, AnalysisScopeOnly)
	}
	if len(report.Analysis.CompletedStages) != 1 || report.Analysis.CompletedStages[0] != "diff" {
		t.Fatalf("unexpected completed stages: %v", report.Analysis.CompletedStages)
	}
}
