package report

import (
	"bytes"
	"fmt"
	"html"
	"html/template"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/Molly166/AegisCodeAgent/internal/githubreport"
	"github.com/Molly166/AegisCodeAgent/internal/review"
)

func daylightReport(findings ...review.Finding) review.ReviewReport {
	result := review.NewReport(review.Comparison{
		Repository: "owner/example", Base: "master", Head: "feature/request-validation",
		BaseCommit: strings.Repeat("a", 40), HeadCommit: strings.Repeat("b", 40),
	}, []review.ChangedFile{{
		NewPath: "internal/request/validate.go", Status: review.FileStatusModified,
		Stats: review.FileStats{Additions: 4, Deletions: 2},
	}}, findings)
	result.Context = review.EmptyContextBundle(review.ContextComplete)
	result.Agent = review.EmptyAgentRun(review.AgentComplete)
	result.Verification = review.EmptyVerificationRun(review.VerificationComplete)
	return result
}

func renderDaylight(t *testing.T, input review.ReviewReport, options githubreport.Options) string {
	t.Helper()
	output, err := RenderHTMLWithOptions(input, options)
	if err != nil {
		t.Fatalf("RenderHTMLWithOptions() error = %v", err)
	}
	return string(output)
}

func daylightPolicy() githubreport.Options {
	return githubreport.Options{
		FailOn: githubreport.PriorityP1, FailOnNeedsReview: githubreport.PriorityP0,
		FailOnIncomplete: true,
	}
}

func TestDaylightPrioritizesEvidenceWithoutMutatingInput(t *testing.T) {
	input := daylightReport(
		review.Finding{Title: "advisory-first", Severity: review.SeverityMedium},
		review.Finding{Title: "critical-second", Severity: review.SeverityCritical},
		review.Finding{Title: "advisory-third", Severity: review.SeverityMedium},
	)
	document := renderDaylight(t, input, daylightPolicy())
	if !(strings.Index(document, ">critical-second</h3>") < strings.Index(document, ">advisory-first</h3>") &&
		strings.Index(document, ">advisory-first</h3>") < strings.Index(document, ">advisory-third</h3>")) {
		t.Fatal("findings should be stable-sorted from P0 to P3")
	}
	if input.Findings[0].Title != "advisory-first" || input.Findings[1].Title != "critical-second" {
		t.Fatal("rendering must not reorder the input report")
	}
}

// Match semantic classes on elements, not class names embedded in the stylesheet.
func daylightHasClass(document, class string) bool {
	return regexp.MustCompile(`\bclass="[^"]*\b` + regexp.QuoteMeta(class) + `\b[^"]*"`).MatchString(document)
}

func TestDaylightEscapesUntrustedReviewEvidence(t *testing.T) {
	payloads := map[string]string{
		"title":       `<img src=x onerror=alert("finding")> & finding`,
		"description": `<script>alert("description")</script> & description`,
		"evidence":    `</pre><script>alert("evidence")</script> & evidence`,
		"suggestion":  `<a href="javascript:alert(1)">fix</a> & suggestion`,
		"path":        `internal/<svg onload=alert(1)>.go`,
		"source":      `<iframe src="https://example.invalid">source</iframe>`,
		"intent":      `<script>alert("intent")</script> & change title`,
		"candidate":   `<script>alert("candidate")</script> & candidate`,
		"snapshot":    `84 | return "</pre><img src=x onerror=alert('snapshot')>"`,
		"check":       `<button onclick="alert('check')">check</button>`,
	}
	input := daylightReport(review.Finding{
		ID:    `same-id" autofocus onfocus="alert(1)`,
		Title: payloads["title"], Description: payloads["description"],
		Severity: review.SeverityHigh, Category: review.CategorySecurity,
		Location: review.Location{Path: payloads["path"], StartLine: 84},
		Evidence: payloads["evidence"], Suggestion: payloads["suggestion"],
		Source: payloads["source"], Confidence: 0.9,
	})
	input.Context.Intent.Source = "pull_request"
	input.Context.Intent.Title = payloads["intent"]
	input.Agent.Candidates = []review.CandidateFinding{{
		ID: "candidate-1", Title: payloads["candidate"], Severity: review.SeverityMedium,
	}}
	input.Verification.Summary = review.VerificationSummary{Candidates: 1, NeedsReview: 1}
	input.Verification.Candidates = []review.CandidateVerification{{
		CandidateID: "candidate-1", Title: payloads["candidate"], Severity: review.SeverityMedium,
		Verdict: review.CandidateNeedsReview, SourceSnapshot: payloads["snapshot"],
		Reason: "A focused check cannot establish the runtime authorization contract.",
		Checks: []review.VerificationCheck{{Name: "semantic-evidence", Status: review.VerificationCheckWarning, Detail: payloads["check"]}},
	}}

	document := renderDaylight(t, input, daylightPolicy())
	for name, payload := range payloads {
		if strings.Contains(document, payload) {
			t.Errorf("%s was rendered as raw HTML", name)
		}
		if !strings.Contains(document, html.EscapeString(payload)) {
			t.Errorf("%s was dropped instead of being preserved as escaped text", name)
		}
	}
	if regexp.MustCompile(`(?i)<(?:script|iframe|img|button)\b`).MatchString(stripTrustedReportLogo(t, document)) {
		t.Fatal("untrusted review content created an active HTML element")
	}
}

func TestDaylightOfflineNavigationUsesRendererOwnedFindingIDs(t *testing.T) {
	findings := make([]review.Finding, 3)
	for i := range findings {
		findings[i] = review.Finding{
			// Duplicate, reserved and quote-bearing provider IDs cannot own document anchors.
			ID:    []string{"findings-heading", "findings-heading", `x" onfocus="alert(1)`}[i],
			Title: fmt.Sprintf("Evidence item %d", i), Severity: review.SeverityMedium,
			Location:    review.Location{Path: "internal/request/validate.go", StartLine: 20 + i},
			Description: fmt.Sprintf("Complete recorded explanation %d", i),
			Evidence:    fmt.Sprintf("source-evidence-%d", i), Suggestion: fmt.Sprintf("recommendation-%d", i),
		}
	}
	document := renderDaylight(t, daylightReport(findings...), daylightPolicy())
	ids := make(map[string]int)
	for _, match := range regexp.MustCompile(`\bid="([^"]+)"`).FindAllStringSubmatch(document, -1) {
		ids[html.UnescapeString(match[1])]++
	}
	for id, count := range ids {
		if count != 1 {
			t.Errorf("duplicate document ID %q appears %d times", id, count)
		}
	}
	for _, match := range regexp.MustCompile(`\bhref="#([^"]+)"`).FindAllStringSubmatch(document, -1) {
		if id := html.UnescapeString(match[1]); ids[id] != 1 {
			t.Errorf("native anchor %q does not resolve to exactly one element", id)
		}
	}
	var radioGroup string
	for i, finding := range findings {
		id := fmt.Sprintf("finding-%d", i)
		control := fmt.Sprintf("finding-select-%d", i)
		if ids[id] != 1 || ids[control] != 1 {
			t.Errorf("finding %d lacks renderer-owned navigation IDs %q and %q", i, control, id)
		}
		input := regexp.MustCompile(`<input\b[^>]*\bid="` + control + `"[^>]*>`).FindString(document)
		if input == "" || !strings.Contains(input, `type="radio"`) || !strings.Contains(input, `aria-controls="`+id+`"`) {
			t.Errorf("finding %d requires a native radio controlling its evidence article", i)
		}
		group := regexp.MustCompile(`\bname="([^"]+)"`).FindStringSubmatch(input)
		if len(group) != 2 {
			t.Errorf("finding %d has no native radio group", i)
		} else if radioGroup == "" {
			radioGroup = group[1]
		} else if radioGroup != group[1] {
			t.Errorf("finding %d belongs to a different radio group", i)
		}
		if i == 0 && !regexp.MustCompile(`\bchecked(?:\s|=|>)`).MatchString(input) {
			t.Error("first finding must have a default native selection")
		}
		if !regexp.MustCompile(`<label\b[^>]*\bfor="` + control + `"`).MatchString(document) {
			t.Errorf("finding %d has no accessible native selection label", i)
		}
		for _, evidence := range []string{finding.Title, finding.Description, finding.Evidence, finding.Suggestion} {
			if !strings.Contains(document, evidence) {
				t.Errorf("offline document omitted %q", evidence)
			}
		}
	}
	for _, tag := range regexp.MustCompile(`<\w+\b[^>]*>`).FindAllString(document, -1) {
		if regexp.MustCompile(`(?i)^<script\b|\bon(?:click|change|load|error|focus)\s*=`).MatchString(tag) {
			t.Fatal("offline report navigation must not require JavaScript or active event handlers")
		}
	}
	if regexp.MustCompile(`(?i)<link\b[^>]*href\s*=|@import\b|\b(?:src|srcset)\s*=\s*["'](?:https?:)?//|url\(\s*["']?(?:https?:)?//`).MatchString(document) {
		t.Fatal("self-contained report unexpectedly depends on remote assets or fonts")
	}
	for _, policy := range []string{"Content-Security-Policy", "default-src 'none'", "base-uri 'none'", "form-action 'none'"} {
		if !strings.Contains(document, policy) {
			t.Errorf("missing offline report protection %q", policy)
		}
	}
}

func TestDaylightMainTitleUsesChangeIntentAndSafeFallbacks(t *testing.T) {
	for _, tc := range []struct {
		name   string
		intent string
		files  []review.ChangedFile
		want   string
	}{
		{name: "intent_is_preferred_and_trimmed", intent: "  Tighten request validation  ",
			files: []review.ChangedFile{{NewPath: "unrelated.go"}}, want: "Tighten request validation"},
		{name: "changed_file_fallback", files: []review.ChangedFile{{NewPath: "internal/request/validate.go"}},
			want: "变更 internal/request/validate.go"},
		{name: "deleted_file_fallback", intent: " \n\t ", files: []review.ChangedFile{{OldPath: "internal/cache/removed.go", Status: review.FileStatusDeleted}},
			want: "变更 internal/cache/removed.go"},
		{name: "empty_scope_fallback", want: "代码评审"},
		{name: "untrusted_intent_stays_text", intent: `<img src=x onerror=alert(1)> & change`,
			want: `<img src=x onerror=alert(1)> & change`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			input := daylightReport()
			input.Context.Intent.Title = tc.intent
			input.Files = tc.files
			document := renderDaylight(t, input, daylightPolicy())
			heading := regexp.MustCompile(`(?s)<h1\b[^>]*>(.*?)</h1>`).FindStringSubmatch(document)
			if len(heading) != 2 {
				t.Fatal("report needs a primary change title")
			}
			if got := strings.TrimSpace(heading[1]); got != html.EscapeString(tc.want) {
				t.Errorf("main heading = %q, want escaped %q", got, tc.want)
			}
		})
	}
}

func TestDaylightP2OutcomeFollowsConfiguredThreshold(t *testing.T) {
	input := daylightReport(review.Finding{
		ID: "P2-1", Title: "Incomplete error handling", Severity: review.SeverityMedium,
		Location: review.Location{Path: "internal/request/validate.go", StartLine: 20},
		Evidence: "The returned error is ignored.",
	})
	for _, tc := range []struct {
		name      string
		threshold githubreport.Priority
		class     string
	}{
		{name: "default_P1_is_advisory", threshold: githubreport.PriorityP1, class: "risk-advisory"},
		{name: "explicit_P2_is_blocking", threshold: githubreport.PriorityP2, class: "risk-blocked"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			options := daylightPolicy()
			options.FailOn = tc.threshold
			document := renderDaylight(t, input, options)
			if !daylightHasClass(document, tc.class) {
				t.Errorf("rendered verdict does not expose %s for threshold %s", tc.class, tc.threshold)
			}
			if !strings.Contains(document, "P2") || !strings.Contains(document, input.Findings[0].Title) {
				t.Fatal("non-blocking policy must not remove a P2 finding from the report")
			}
		})
	}
}

func TestDaylightIncompleteAndUnresolvedEvidenceNeverClaimClean(t *testing.T) {
	for _, tc := range []struct {
		name       string
		failClosed bool
		prepare    func(*review.ReviewReport)
		class      string
		visible    string
	}{
		{name: "partial_required_analysis", failClosed: true, class: "risk-incomplete", visible: "Review incomplete", prepare: func(r *review.ReviewReport) {
			r.Analysis.Status = review.AnalysisPartial
		}},
		{name: "partial_explicitly_allowed", class: "risk-degraded", visible: "Review incomplete", prepare: func(r *review.ReviewReport) {
			r.Analysis.Status = review.AnalysisPartial
		}},
		{name: "partial_optional_reasoning", failClosed: true, class: "risk-degraded", visible: "Review degraded", prepare: func(r *review.ReviewReport) {
			r.Agent.Status = review.AgentPartial
			r.Agent.Warnings = []string{"The model reached the configured tool-call budget."}
		}},
		{name: "unresolved_P0", failClosed: true, class: "risk-needs-review", visible: "Human review required", prepare: func(r *review.ReviewReport) {
			r.Verification.Summary = review.VerificationSummary{Candidates: 1, NeedsReview: 1}
			r.Verification.Candidates = []review.CandidateVerification{{
				CandidateID: "candidate-auth", Title: "Unresolved authorization hypothesis", Severity: review.SeverityCritical,
				Verdict: review.CandidateNeedsReview, Reason: "Runtime permissions were unavailable to the verifier.",
			}}
		}},
		{name: "legacy_inconclusive_P1", failClosed: true, class: "risk-needs-review", visible: "Human review required", prepare: func(r *review.ReviewReport) {
			r.Verification.Summary = review.VerificationSummary{Candidates: 1, Inconclusive: 1}
			r.Verification.Candidates = []review.CandidateVerification{{
				CandidateID: "legacy-auth", Title: "Legacy unresolved hypothesis", Severity: review.SeverityHigh,
				Verdict: review.CandidateInconclusive, Reason: "The stored verdict predates needs-review terminology.",
			}}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			input := daylightReport()
			tc.prepare(&input)
			options := daylightPolicy()
			options.FailOnIncomplete = tc.failClosed
			document := renderDaylight(t, input, options)
			if !daylightHasClass(document, tc.class) || !strings.Contains(document, tc.visible) {
				t.Errorf("report must expose %s with visible state %q", tc.class, tc.visible)
			}
			if daylightHasClass(document, "risk-clear") || strings.Contains(document, "Ready to merge") || strings.Contains(document, "All requested review stages completed without evidence-bearing findings") {
				t.Fatal("partial or unresolved review was presented as a clean review")
			}
			for _, candidate := range input.Verification.Candidates {
				if !strings.Contains(document, candidate.Title) || !strings.Contains(document, candidate.Reason) {
					t.Fatal("unresolved verification evidence disappeared from the document")
				}
			}
			for _, warning := range input.Agent.Warnings {
				if !strings.Contains(document, warning) {
					t.Fatal("reduced-coverage warning disappeared from the document")
				}
			}
		})
	}
}

func TestReportTextBlocksPreservePlainAndFencedPayloads(t *testing.T) {
	for _, tc := range []struct {
		name  string
		input string
		want  []reportTextBlock
	}{
		{name: "empty", want: []reportTextBlock{}},
		{name: "plain_is_not_markdown", input: "**Important**\n[link](javascript:alert(1))\n", want: []reportTextBlock{{Text: "**Important**\n[link](javascript:alert(1))\n"}}},
		{name: "paragraph_code_paragraph", input: "Validate the input.\n\n```go\nif err != nil {\n\treturn err\n}\n```\nThen retry.\n", want: []reportTextBlock{
			{Text: "Validate the input.\n\n"}, {Code: true, Text: "if err != nil {\n\treturn err\n}\n"}, {Text: "Then retry.\n"},
		}},
		{name: "CRLF_and_indent_are_preserved", input: "Before\r\n  ```go\r\n  return nil\r\n  ```\r\nAfter", want: []reportTextBlock{
			{Text: "Before\r\n"}, {Code: true, Text: "  return nil\r\n"}, {Text: "After"},
		}},
		{name: "multiple_blocks", input: "```go\nfirst()\n```\nBetween\n```text\nsecond\n```", want: []reportTextBlock{
			{Code: true, Text: "first()\n"}, {Text: "Between\n"}, {Code: true, Text: "second\n"},
		}},
		{name: "empty_code_block", input: "```\n```", want: []reportTextBlock{{Code: true, Text: ""}}},
		{name: "inline_ticks_stay_literal", input: "Replace ```go x``` with a checked return.", want: []reportTextBlock{{Text: "Replace ```go x``` with a checked return."}}},
		{name: "unclosed_fence_has_no_loss", input: "Before\n```go\nreturn value\n", want: []reportTextBlock{{Text: "Before\n```go\nreturn value\n"}}},
		{name: "unclosed_after_valid_fence_has_no_loss", input: "```go\na()\n```\nAfter\n```go\nb()", want: []reportTextBlock{
			{Code: true, Text: "a()\n"}, {Text: "After\n```go\nb()"},
		}},
		{name: "four_ticks_stay_literal", input: "````go\nreturn value\n````", want: []reportTextBlock{{Text: "````go\nreturn value\n````"}}},
		{name: "malformed_info_stays_literal", input: "```go`bad\nreturn value\n```", want: []reportTextBlock{{Text: "```go`bad\nreturn value\n```"}}},
		{name: "indented_fence_stays_literal", input: "    ```go\n    return value\n    ```", want: []reportTextBlock{{Text: "    ```go\n    return value\n    ```"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := reportTextBlocks(tc.input); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("reportTextBlocks() = %#v, want %#v", got, tc.want)
			}
		})
	}
}

func TestReportTextBlocksRemainEscapedInHTMLTemplate(t *testing.T) {
	plain := `<img src=x onerror=alert("plain")> & prose`
	code := `</pre><script>alert("code")</script> & source`
	input := plain + "\n```html\n" + code + "\n```\n"
	blocks := reportTextBlocks(input)
	if len(blocks) != 2 || blocks[0].Text != plain+"\n" || !blocks[1].Code || blocks[1].Text != code+"\n" {
		t.Fatalf("malicious payload was changed or discarded: %#v", blocks)
	}
	view := template.Must(template.New("safe-blocks").Parse(`{{range .}}{{if .Code}}<pre>{{.Text}}</pre>{{else}}<p>{{.Text}}</p>{{end}}{{end}}`))
	var output bytes.Buffer
	if err := view.Execute(&output, blocks); err != nil {
		t.Fatal(err)
	}
	document := output.String()
	for _, payload := range []string{plain, code} {
		if strings.Contains(document, payload) || !strings.Contains(document, html.EscapeString(payload)) {
			t.Errorf("payload was not safely preserved as escaped text: %q", payload)
		}
	}
	if strings.Contains(document, "<script") || strings.Contains(document, "<img") {
		t.Fatal("fenced text became active HTML")
	}
}
