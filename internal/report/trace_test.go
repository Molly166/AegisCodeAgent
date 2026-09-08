package report

import (
	"strings"
	"testing"

	"github.com/Molly166/AegisCodeAgent/internal/githubreport"
	"github.com/Molly166/AegisCodeAgent/internal/review"
)

func TestHTMLCompletionTraceEscapesProviderMetadata(t *testing.T) {
	r := review.NewReport(review.Comparison{}, nil, nil)
	r.Agent = review.EmptyAgentRun(review.AgentComplete)
	r.Agent.Completions = []review.AgentCompletionTrace{{Step: 1, CompletionMetadata: review.CompletionMetadata{
		Provider: "orcarouter", RequestedModel: "chosen-model", RequestID: "<script>alert(1)</script>", Attempts: 2, HTTPStatus: 200, DurationMillis: 150,
	}}}
	data, err := RenderHTML(r)
	if err != nil {
		t.Fatal(err)
	}
	output := string(data)
	for _, required := range []string{"model requests", "chosen-model", "Unknown", "2 attempt(s)", "Content-Security-Policy", "&lt;script&gt;"} {
		if !strings.Contains(output, required) {
			t.Error("missing trace detail", required)
		}
	}
	if strings.Contains(output, "<script>alert") {
		t.Fatal("unescaped provider response metadata")
	}
}

func TestScopeOnlyReportDoesNotMaskGateDecision(t *testing.T) {
	r := review.NewScopeReport(review.Comparison{}, nil)
	gate := githubreport.EvaluateWithOptions(r, githubreport.Options{FailOnIncomplete: true})
	v := htmlVerdictFor(r, gate, githubreport.Options{FailOnIncomplete: true})
	if v.Label != "Blocked" {
		t.Fatalf("scope-only report hid incomplete gate: %+v", v)
	}
	r.Findings = []review.Finding{{Severity: review.SeverityCritical}}
	gate = githubreport.EvaluateWithOptions(r, githubreport.Options{FailOn: githubreport.PriorityP1})
	v = htmlVerdictFor(r, gate, githubreport.Options{FailOn: githubreport.PriorityP1})
	if v.Label != "Blocked" || v.Count != "P0 finding" {
		t.Fatalf("scope-only report hid independent P0 finding: %+v", v)
	}
}
