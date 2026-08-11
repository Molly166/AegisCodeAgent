package analyzer

import (
	"context"
	"testing"
	"time"

	"github.com/Molly166/AegisCodeAgent/internal/review"
)

type pipelineStub struct {
	name     string
	findings []review.Finding
	err      error
	wait     bool
}

func (a pipelineStub) Name() string { return a.name }

func (a pipelineStub) Analyze(ctx context.Context, _ Input) ([]review.Finding, error) {
	if a.wait {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	return append([]review.Finding(nil), a.findings...), a.err
}

func TestPipelineFiltersDeduplicatesAndSortsFindings(t *testing.T) {
	files := []review.ChangedFile{{
		NewPath: "main.go",
		Hunks: []review.Hunk{{Lines: []review.DiffLine{
			{Kind: review.LineAddition, NewLine: 10},
		}}},
	}}
	high := review.Finding{Title: "high", RuleID: "SA1", Severity: review.SeverityHigh, Source: NameStaticcheck, Location: review.Location{Path: "main.go", StartLine: 10}, Confidence: 2}
	unchanged := review.Finding{Title: "unchanged", RuleID: "S1", Severity: review.SeverityLow, Source: NameStaticcheck, Location: review.Location{Path: "main.go", StartLine: 20}}
	goTest := review.Finding{Title: "test failed", Severity: review.SeverityHigh, Source: NameGoTest, Location: review.Location{Path: "main_test.go", StartLine: 30}}
	pipeline := NewPipeline(
		pipelineStub{name: "static", findings: []review.Finding{high, high, unchanged}},
		pipelineStub{name: "tests", findings: []review.Finding{goTest}},
	)
	output, err := pipeline.Run(context.Background(), Input{
		Repository:   "/repo",
		ChangedLines: BuildChangedLineSet("/repo", files),
	}, []string{"static", "tests"}, RunOptions{OnlyChangedLines: true})
	if err != nil {
		t.Fatal(err)
	}
	if output.Analysis.Status != review.AnalysisComplete {
		t.Fatalf("analysis status = %q", output.Analysis.Status)
	}
	if output.Analysis.Tools[0].Suppressed != 1 {
		t.Fatalf("suppressed = %d, want 1", output.Analysis.Tools[0].Suppressed)
	}
	if len(output.Findings) != 2 {
		t.Fatalf("finding count = %d, want 2: %+v", len(output.Findings), output.Findings)
	}
	for _, finding := range output.Findings {
		if finding.ID == "" || finding.Fingerprint == "" {
			t.Fatalf("finding does not have stable identity: %+v", finding)
		}
		if finding.Confidence > 1 {
			t.Fatalf("confidence was not clamped: %+v", finding)
		}
	}
}

func TestPipelineMarksUnavailableAnalyzerPartial(t *testing.T) {
	pipeline := NewPipeline(
		pipelineStub{name: "ok"},
		pipelineStub{name: "missing", err: &SkipError{Kind: SkipUnavailable, Reason: "not installed"}},
	)
	output, err := pipeline.Run(context.Background(), Input{}, []string{"ok", "missing"}, RunOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if output.Analysis.Status != review.AnalysisPartial || output.Analysis.Tools[1].Status != review.ToolUnavailable {
		t.Fatalf("unexpected analysis: %+v", output.Analysis)
	}
}

func TestPipelineTimesOutAnalyzer(t *testing.T) {
	pipeline := NewPipeline(pipelineStub{name: "slow", wait: true})
	output, err := pipeline.Run(context.Background(), Input{}, []string{"slow"}, RunOptions{AnalyzerTimeout: time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	if output.Analysis.Status != review.AnalysisFailed || output.Analysis.Tools[0].Status != review.ToolTimedOut {
		t.Fatalf("unexpected analysis: %+v", output.Analysis)
	}
}

func TestPipelineWithoutAnalyzersIsScopeOnly(t *testing.T) {
	output, err := NewPipeline().Run(context.Background(), Input{}, nil, RunOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if output.Analysis.Status != review.AnalysisScopeOnly || output.Findings == nil || output.Analysis.Tools == nil {
		t.Fatalf("unexpected output: %+v", output)
	}
}
