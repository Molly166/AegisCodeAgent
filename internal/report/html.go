package report

import (
	"bytes"
	_ "embed"
	"fmt"
	"html/template"
	"strings"
	"time"

	"github.com/Molly166/AegisCodeAgent/internal/githubreport"
	"github.com/Molly166/AegisCodeAgent/internal/review"
)

//go:embed report_v2.css
var htmlReportV2CSS string

type htmlReportView struct {
	review.ReviewReport
	Counts  githubreport.Counts
	Gate    githubreport.GateResult
	Options githubreport.Options
	Verdict htmlVerdict
}

type htmlVerdict struct {
	Class           string
	Title           string
	Label           string
	Count           string
	Caption         string
	Summary         string
	DecisionTitle   string
	DecisionMessage string
}

var htmlReportTemplate = template.Must(template.New("review-report").Funcs(template.FuncMap{
	"formatTime":        func(value time.Time) string { return value.UTC().Format("02 Jan 2006 · 15:04 UTC") },
	"shortCommit":       shortCommit,
	"location":          formatLocation,
	"confidence":        func(value float64) string { return fmt.Sprintf("%.0f%%", value*100) },
	"riskClass":         riskClass,
	"riskLabel":         riskLabel,
	"analysisLabel":     analysisLabel,
	"emptyTitle":        emptyTitle,
	"emptyMessage":      emptyMessage,
	"duration":          durationMillis,
	"toolLabel":         toolStatusLabel,
	"contextLabel":      contextStatusLabel,
	"agentLabel":        agentStatusLabel,
	"verificationLabel": verificationStatusLabel,
	"verdictLabel":      candidateVerdictLabel,
	"priorityLabel":     func(value githubreport.Priority) string { return strings.ToUpper(string(value)) },
	"reportCSS":         func() template.CSS { return template.CSS(htmlReportV2CSS) },
	"needsReviewCount": func(run review.VerificationRun) int {
		return run.Summary.NeedsReview + run.Summary.Inconclusive
	},
	"symbolLocation": formatSymbolLocation,
	"join":           strings.Join,
	"hasSymbolDetail": func(symbol review.ContextSymbol) bool {
		return symbol.Documentation != "" || symbol.Snippet != ""
	},
	"severityLabel": severityLabel,
	"statusLabel":   statusLabel,
	"hasDetails":    func(finding review.Finding) bool { return finding.Evidence != "" || finding.Suggestion != "" },
	"hasCandidateDetails": func(candidate review.CandidateFinding) bool {
		return candidate.Evidence != "" || candidate.Suggestion != "" || len(candidate.Verification) > 0
	},
}).Parse(htmlTemplateSource))

func RenderHTML(reviewReport review.ReviewReport) ([]byte, error) {
	return RenderHTMLWithOptions(reviewReport, githubreport.Options{
		FailOn:            githubreport.PriorityP1,
		FailOnNeedsReview: githubreport.PriorityP0,
		FailOnIncomplete:  true,
	})
}

func RenderHTMLWithOptions(reviewReport review.ReviewReport, options githubreport.Options) ([]byte, error) {
	if options.FailOn == "" {
		options.FailOn = githubreport.PriorityP1
	}
	if options.FailOnNeedsReview == "" {
		options.FailOnNeedsReview = githubreport.PriorityP0
	}
	gate := githubreport.EvaluateWithOptions(reviewReport, options)
	view := htmlReportView{
		ReviewReport: reviewReport,
		Counts:       githubreport.Count(reviewReport),
		Gate:         gate,
		Options:      options,
		Verdict:      htmlVerdictFor(reviewReport, gate, options),
	}
	var output bytes.Buffer
	if err := htmlReportTemplate.Execute(&output, view); err != nil {
		return nil, fmt.Errorf("render HTML report: %w", err)
	}
	return output.Bytes(), nil
}

func htmlVerdictFor(report review.ReviewReport, gate githubreport.GateResult, options githubreport.Options) htmlVerdict {
	findings := fmt.Sprintf("%d findings", len(report.Findings))
	if len(report.Findings) == 1 {
		findings = "1 finding"
	}
	if report.Analysis.Status == review.AnalysisScopeOnly {
		return htmlVerdict{
			Class: "pending", Title: "Review pending", Label: "Pending", Count: "Scope mapped", Caption: "Analysis pending",
			Summary:         "The comparison scope is available, but deterministic analysis and evidence verification have not run.",
			DecisionTitle:   "Merge decision pending analysis.",
			DecisionMessage: "Run the configured analyzers and requested review stages before interpreting this report as a safety verdict.",
		}
	}
	switch {
	case gate.Incomplete && options.FailOnIncomplete:
		return htmlVerdict{
			Class: "incomplete", Title: "Review incomplete", Label: "Blocked", Count: "merge gate blocked", Caption: "Rerun required",
			Summary:         "The merge gate is blocked because one or more required review stages did not complete.",
			DecisionTitle:   "Merge gate blocked by an incomplete review.",
			DecisionMessage: "Restore the incomplete stage and rerun Aegis. Findings from completed stages remain visible, but this report must not be interpreted as a clean review.",
		}
	case gate.BlockedByFinding:
		priority := strings.ToUpper(string(gate.Highest))
		threshold := strings.ToUpper(string(options.FailOn))
		return htmlVerdict{
			Class: "blocked", Title: "Merge blocked", Label: "Blocked", Count: priority + " finding", Caption: "Fix required",
			Summary:         fmt.Sprintf("A %s finding met the configured %s merge threshold.", priority, threshold),
			DecisionTitle:   "A verified finding blocks this change.",
			DecisionMessage: "Inspect the evidence below, correct the affected code, and rerun Aegis before merging.",
		}
	case gate.BlockedByNeedsReview:
		priority := strings.ToUpper(string(gate.NeedsReviewHighest))
		return htmlVerdict{
			Class: "needs-review", Title: "Human review required", Label: "Blocked", Count: priority + " unresolved", Caption: "Decision required",
			Summary:         "An unresolved high-risk hypothesis reached the configured needs-review threshold.",
			DecisionTitle:   "Merge gate blocked pending human review.",
			DecisionMessage: "Inspect the unresolved verification evidence and record a human decision before merging.",
		}
	case gate.NeedsReview:
		return htmlVerdict{
			Class: "needs-review", Title: "Human review required", Label: "Review", Count: findings, Caption: "Inspect hypotheses",
			Summary:         "The automated gate passed, but unresolved hypotheses still require human judgment.",
			DecisionTitle:   "Automated checks passed with unresolved hypotheses.",
			DecisionMessage: "Review the inconclusive evidence below before making the final merge decision.",
		}
	case gate.Incomplete:
		return htmlVerdict{
			Class: "degraded", Title: "Review incomplete", Label: "Passed", Count: findings, Caption: "Allowed by policy",
			Summary:         "The merge gate passed because incomplete-stage blocking is disabled, but required review coverage is incomplete.",
			DecisionTitle:   "Merge gate passed with an incomplete review.",
			DecisionMessage: "Inspect the incomplete-stage reasons before merging; the current policy explicitly permits this reduced coverage.",
		}
	case gate.Degraded:
		return htmlVerdict{
			Class: "degraded", Title: "Review degraded", Label: "Passed", Count: findings, Caption: "Coverage reduced",
			Summary:         "The deterministic merge gate passed, but an optional review stage completed with reduced coverage.",
			DecisionTitle:   "Merge gate passed with degraded coverage.",
			DecisionMessage: "Deterministic evidence remains valid. Inspect the degraded-stage reason before relying on optional Agent coverage.",
		}
	case len(report.Findings) > 0:
		return htmlVerdict{
			Class: "advisory", Title: "Review complete", Label: "Passed", Count: findings, Caption: "Advisory findings",
			Summary:         "The merge gate passed. Evidence-bearing findings remain below the configured blocking threshold.",
			DecisionTitle:   "Merge gate passed with advisory findings.",
			DecisionMessage: "Review the non-blocking findings below and decide whether to address them in this change or track them separately.",
		}
	default:
		return htmlVerdict{
			Class: "clear", Title: "Review complete", Label: "Passed", Count: "no findings", Caption: "Ready to merge",
			Summary:         "All requested review stages completed without evidence-bearing findings.",
			DecisionTitle:   "Merge gate passed without findings.",
			DecisionMessage: "No verified issue crossed the configured thresholds for this comparison.",
		}
	}
}

func riskClass(report review.ReviewReport) string {
	if report.Analysis.Status == review.AnalysisScopeOnly {
		return "scope"
	}
	if report.Analysis.Status == review.AnalysisFailed {
		return "critical"
	}
	summary := report.Summary
	switch {
	case summary.Critical > 0:
		return "critical"
	case hasNeedsReviewAt(report, review.SeverityCritical):
		return "critical"
	case report.Context.Status == review.ContextFailed,
		report.Agent.Status == review.AgentFailed,
		report.Verification.Status == review.VerificationFailed:
		return "critical"
	case summary.High > 0:
		return "high"
	case hasNeedsReviewAt(report, review.SeverityHigh):
		return "high"
	case summary.Medium > 0:
		return "medium"
	case hasNeedsReviewAt(report, review.SeverityMedium):
		return "medium"
	case report.Analysis.Status == review.AnalysisPartial,
		report.Context.Status == review.ContextPartial,
		report.Agent.Status == review.AgentPartial,
		report.Verification.Status == review.VerificationPartial:
		return "medium"
	case summary.Low > 0:
		return "low"
	default:
		return "clear"
	}
}

func riskLabel(report review.ReviewReport) string {
	if report.Analysis.Status == review.AnalysisScopeOnly {
		return "Scope mapped"
	}
	if report.Analysis.Status == review.AnalysisFailed {
		return "Analysis failed"
	}
	summary := report.Summary
	switch {
	case summary.Critical > 0:
		return "Block"
	case report.Context.Status == review.ContextFailed:
		return "Context failed"
	case report.Agent.Status == review.AgentFailed:
		return "Reasoning failed"
	case report.Verification.Status == review.VerificationFailed:
		return "Verification failed"
	case hasUnresolvedCandidates(report):
		return "Needs review"
	case summary.High > 0:
		return "Attention"
	case summary.Medium > 0:
		return "Review"
	case report.Analysis.Status == review.AnalysisPartial:
		return "Partial review"
	case report.Context.Status == review.ContextPartial:
		return "Partial context"
	case report.Agent.Status == review.AgentPartial:
		return "Partial reasoning"
	case report.Verification.Status == review.VerificationPartial:
		return "Partial verification"
	case summary.Low > 0:
		return "Low risk"
	case summary.Findings > 0:
		return "Informational"
	default:
		return "No findings"
	}
}

func analysisLabel(analysis review.Analysis) string {
	switch analysis.Status {
	case review.AnalysisScopeOnly:
		return "diff mapped · analysis pending"
	case review.AnalysisFailed:
		return "review incomplete"
	case review.AnalysisPartial:
		return "some analyzers incomplete"
	default:
		return "analysis complete"
	}
}

func emptyTitle(report review.ReviewReport) string {
	if report.Analysis.Status == review.AnalysisScopeOnly {
		return "Change scope is ready for analysis."
	}
	if report.Analysis.Status == review.AnalysisFailed {
		return "Review analysis did not complete."
	}
	if report.Analysis.Status == review.AnalysisPartial {
		return "No findings from completed analyzers."
	}
	if report.Verification.Status == review.VerificationFailed {
		return "Candidate verification did not complete."
	}
	if report.Verification.Status == review.VerificationPartial {
		return "No findings from completed verification checks."
	}
	if hasUnresolvedCandidates(report) {
		return "No verified finding, but human review is required."
	}
	return "No review findings were reported."
}

func emptyMessage(report review.ReviewReport) string {
	if report.Analysis.Status == review.AnalysisScopeOnly {
		return "This foundation report documents the diff. Risk analysis and verification have not run yet."
	}
	if report.Analysis.Status == review.AnalysisFailed {
		return "Inspect analyzer execution details, fix the failed tools, and run the review again."
	}
	if report.Analysis.Status == review.AnalysisPartial {
		return "At least one requested analyzer was unavailable or failed; this is not a complete safety verdict."
	}
	if report.Verification.Status == review.VerificationFailed {
		return "Inspect the verification warnings, restore the focused checks, and run the review again."
	}
	if report.Verification.Status == review.VerificationPartial {
		return "At least one focused verification check was unavailable or failed; unsupported candidates remain withheld."
	}
	if hasUnresolvedCandidates(report) {
		return "One or more hypotheses could not be verified or rejected. This result must not be interpreted as a clean review; inspect the Needs Review cards above."
	}
	return "The completed analysis did not produce findings for this comparison."
}

func hasUnresolvedCandidates(report review.ReviewReport) bool {
	for _, candidate := range report.Verification.Candidates {
		if candidate.Verdict == review.CandidateNeedsReview || candidate.Verdict == review.CandidateInconclusive {
			return true
		}
	}
	return false
}

func hasNeedsReviewAt(report review.ReviewReport, severity review.Severity) bool {
	for _, candidate := range report.Verification.Candidates {
		if (candidate.Verdict == review.CandidateNeedsReview || candidate.Verdict == review.CandidateInconclusive) && candidate.Severity == severity {
			return true
		}
	}
	return false
}

func durationMillis(value int64) string {
	if value < 1000 {
		return fmt.Sprintf("%d ms", value)
	}
	return fmt.Sprintf("%.1f s", float64(value)/1000)
}

func toolStatusLabel(status review.ToolStatus) string {
	return strings.ReplaceAll(strings.ToUpper(string(status)), "_", " ")
}

func contextStatusLabel(status review.ContextStatus) string {
	return strings.ReplaceAll(strings.ToUpper(string(status)), "_", " ")
}

func agentStatusLabel(status review.AgentStatus) string {
	return strings.ReplaceAll(strings.ToUpper(string(status)), "_", " ")
}

func verificationStatusLabel(status review.VerificationStatus) string {
	return strings.ReplaceAll(strings.ToUpper(string(status)), "_", " ")
}

func candidateVerdictLabel(verdict review.CandidateVerdict) string {
	return strings.ReplaceAll(strings.ToUpper(string(verdict)), "_", " ")
}

func severityLabel(severity review.Severity) string {
	if severity == "" {
		return "P3"
	}
	return strings.ToUpper(string(githubreport.PriorityForSeverity(severity)))
}

func statusLabel(status review.FileStatus) string {
	if status == "" {
		return "Unknown"
	}
	return strings.ToUpper(string(status[:1])) + string(status[1:])
}

const htmlTemplateSource = `<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <meta name="color-scheme" content="light">
  <title>Aegis review · {{.Comparison.Base}} → {{.Comparison.Head}}</title>
  <style>
    :root {
      --ink: #18233a;
      --muted: #657087;
      --paper: #f2f5fa;
      --surface: #ffffff;
      --line: #d9e0eb;
      --indigo: #4054b2;
      --cyan: #168ba0;
      --critical: #b4233f;
      --high: #d0523f;
      --medium: #c07a16;
      --low: #3973b7;
      --clear: #22745b;
      --display: "Avenir Next", "Segoe UI Variable Display", "Segoe UI", sans-serif;
      --body: "Avenir Next", "Segoe UI Variable Text", "Segoe UI", sans-serif;
      --mono: "SFMono-Regular", "Cascadia Code", "Roboto Mono", monospace;
    }

    * { box-sizing: border-box; }

    body {
      margin: 0;
      color: var(--ink);
      background:
        linear-gradient(rgba(64, 84, 178, .035) 1px, transparent 1px),
        linear-gradient(90deg, rgba(64, 84, 178, .035) 1px, transparent 1px),
        var(--paper);
      background-size: 28px 28px;
      font-family: var(--body);
      font-size: 15px;
      line-height: 1.55;
    }

    a, summary { -webkit-tap-highlight-color: transparent; }
    code { font-family: var(--mono); }

    .report-shell {
      width: min(1180px, calc(100% - 40px));
      margin: 32px auto 56px;
      background: var(--surface);
      border: 1px solid var(--line);
      box-shadow: 0 24px 70px rgba(30, 43, 70, .10);
      animation: arrive .45s ease-out both;
    }

    .masthead {
      min-height: 64px;
      display: flex;
      align-items: center;
      justify-content: space-between;
      gap: 24px;
      padding: 14px 28px;
      border-bottom: 1px solid var(--line);
    }

    .brand { display: flex; align-items: center; gap: 12px; }
    .brand-mark { width: 31px; height: 35px; color: var(--indigo); }
    .brand-name {
      display: block;
      font: 700 15px/1 var(--display);
      letter-spacing: .01em;
    }
    .brand-kind {
      display: block;
      margin-top: 5px;
      color: var(--muted);
      font: 650 10px/1 var(--mono);
      letter-spacing: .14em;
      text-transform: uppercase;
    }
    .generated { color: var(--muted); font: 500 11px/1.3 var(--mono); }

    .hero {
      display: grid;
      grid-template-columns: minmax(0, 1fr) 248px;
      min-height: 320px;
      border-bottom: 1px solid var(--line);
    }

    .hero-copy { padding: 56px 52px 48px; }
    .eyebrow {
      margin: 0 0 19px;
      color: var(--indigo);
      font: 700 11px/1 var(--mono);
      letter-spacing: .16em;
      text-transform: uppercase;
    }
    h1 {
      max-width: 760px;
      margin: 0;
      font: 650 clamp(34px, 5vw, 62px)/1.02 var(--display);
      letter-spacing: -.045em;
    }
    .comparison-arrow { color: var(--cyan); font-weight: 400; }
    .repository {
      max-width: 760px;
      margin: 23px 0 0;
      overflow-wrap: anywhere;
      color: var(--muted);
      font: 500 13px/1.6 var(--mono);
    }
    .commits { display: flex; flex-wrap: wrap; gap: 8px; margin-top: 25px; }
    .commit {
      padding: 6px 9px;
      color: #4b5670;
      background: #f6f7fb;
      border: 1px solid #e0e5ee;
      font: 550 11px/1 var(--mono);
    }

    .verdict-panel {
      display: grid;
      place-items: center;
      padding: 28px;
      color: white;
      background: var(--indigo);
      position: relative;
      overflow: hidden;
    }
    .verdict-panel::before,
    .verdict-panel::after {
      content: "";
      position: absolute;
      border: 1px solid rgba(255,255,255,.16);
      border-radius: 50%;
    }
    .verdict-panel::before { width: 260px; height: 260px; }
    .verdict-panel::after { width: 184px; height: 184px; }
    .verdict-panel.risk-critical { background: var(--critical); }
    .verdict-panel.risk-high { background: var(--high); }
    .verdict-panel.risk-medium { background: var(--medium); }
    .verdict-panel.risk-low { background: var(--low); }
    .verdict-panel.risk-clear { background: var(--clear); }
    .verdict-panel.risk-scope { background: var(--indigo); }
    .verdict {
      width: 144px;
      height: 164px;
      display: flex;
      flex-direction: column;
      align-items: center;
      justify-content: center;
      position: relative;
      z-index: 1;
      clip-path: polygon(50% 0, 94% 17%, 86% 71%, 50% 100%, 14% 71%, 6% 17%);
      background: rgba(11, 24, 46, .23);
      border: 1px solid rgba(255,255,255,.55);
      text-align: center;
    }
    .verdict-label { font: 750 19px/1.05 var(--display); letter-spacing: -.02em; }
    .verdict-count { margin-top: 10px; font: 600 10px/1.2 var(--mono); letter-spacing: .11em; text-transform: uppercase; opacity: .78; }

    .metrics {
      display: grid;
      grid-template-columns: repeat(4, 1fr);
      border-bottom: 1px solid var(--line);
    }
    .metric { padding: 25px 28px 23px; border-right: 1px solid var(--line); }
    .metric:last-child { border-right: 0; }
    .metric-label { display: block; color: var(--muted); font: 650 10px/1 var(--mono); letter-spacing: .12em; text-transform: uppercase; }
    .metric-value { display: block; margin-top: 9px; font: 650 27px/1 var(--display); letter-spacing: -.03em; }
    .metric-value .plus { color: var(--clear); }
    .metric-value .minus { color: var(--high); }

    .section { padding: 42px 52px 48px; border-bottom: 1px solid var(--line); }
    .section:last-child { border-bottom: 0; }
    .section-heading { display: flex; align-items: end; justify-content: space-between; gap: 20px; margin-bottom: 22px; }
    h2 { margin: 0; font: 650 25px/1.1 var(--display); letter-spacing: -.025em; }
    .section-note { margin: 0; color: var(--muted); font: 500 11px/1.4 var(--mono); }

    .file-table { border-top: 1px solid var(--ink); }
    .file-row {
      min-height: 61px;
      display: grid;
      grid-template-columns: 104px minmax(0, 1fr) 74px 74px 62px;
      align-items: center;
      gap: 12px;
      border-bottom: 1px solid var(--line);
    }
    .status {
      width: max-content;
      padding: 5px 8px;
      color: var(--indigo);
      background: #eef0fb;
      font: 700 9px/1 var(--mono);
      letter-spacing: .08em;
      text-transform: uppercase;
    }
    .file-path { min-width: 0; overflow-wrap: anywhere; font: 560 13px/1.45 var(--mono); }
    .delta { text-align: right; font: 600 12px/1 var(--mono); }
    .delta.add { color: var(--clear); }
    .delta.remove { color: var(--high); }
    .binary { color: var(--muted); text-align: right; font: 650 9px/1 var(--mono); letter-spacing: .08em; text-transform: uppercase; }

    .risk-spine { position: relative; padding-left: 32px; }
    .risk-spine::before { content: ""; position: absolute; inset: 9px auto 9px 7px; width: 1px; background: var(--line); }
    .finding {
      --severity: var(--muted);
      position: relative;
      margin-bottom: 16px;
      padding: 22px 24px 21px;
      background: #fbfcfe;
      border: 1px solid var(--line);
      border-left: 3px solid var(--severity);
    }
    .finding:last-child { margin-bottom: 0; }
    .finding::before { content: ""; position: absolute; top: 27px; left: -31px; width: 11px; height: 11px; background: var(--surface); border: 3px solid var(--severity); border-radius: 50%; }
    .finding.severity-critical { --severity: var(--critical); }
    .finding.severity-high { --severity: var(--high); }
    .finding.severity-medium { --severity: var(--medium); }
    .finding.severity-low { --severity: var(--low); }
    .finding.severity-info { --severity: var(--cyan); }
    .finding-top { display: flex; align-items: start; justify-content: space-between; gap: 20px; }
    .finding h3 { margin: 0; font: 650 17px/1.35 var(--display); letter-spacing: -.015em; }
    .severity { flex: none; padding: 5px 8px; color: var(--severity); border: 1px solid currentColor; font: 750 9px/1 var(--mono); letter-spacing: .1em; text-transform: uppercase; }
    .finding-meta { display: flex; flex-wrap: wrap; gap: 7px 14px; margin: 12px 0 0; color: var(--muted); font: 500 11px/1.4 var(--mono); }
    .finding-description { margin: 15px 0 0; max-width: 820px; color: #3e4960; }
    details { margin-top: 17px; border-top: 1px solid var(--line); }
    summary { width: max-content; padding-top: 14px; color: var(--indigo); cursor: pointer; font: 700 10px/1.2 var(--mono); letter-spacing: .09em; text-transform: uppercase; }
    summary:focus-visible { outline: 3px solid rgba(64,84,178,.28); outline-offset: 4px; }
    .detail-grid { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 18px; padding-top: 17px; }
    .detail-block { min-width: 0; }
    .detail-block h4 { margin: 0 0 7px; color: var(--muted); font: 700 9px/1 var(--mono); letter-spacing: .1em; text-transform: uppercase; }
    .detail-block p, .detail-block pre { margin: 0; white-space: pre-wrap; overflow-wrap: anywhere; color: #344056; font: 500 12px/1.6 var(--mono); }

    .empty-state {
      display: grid;
      grid-template-columns: 50px minmax(0, 1fr);
      gap: 18px;
      align-items: center;
      padding: 24px;
      color: #375348;
      background: #f1f8f5;
      border: 1px solid #cfe5db;
    }
    .empty-icon { width: 44px; height: 49px; color: var(--clear); }
    .empty-state strong { display: block; font: 650 16px/1.2 var(--display); }
    .empty-state p { margin: 6px 0 0; color: #60756d; }
    .no-files { padding: 24px; color: var(--muted); background: #f8f9fc; border-top: 1px solid var(--ink); border-bottom: 1px solid var(--line); }

    .tool-table { border-top: 1px solid var(--ink); }
    .tool-row {
      min-height: 61px;
      display: grid;
      grid-template-columns: 150px 118px 84px 90px 96px minmax(0, 1fr);
      align-items: center;
      gap: 14px;
      border-bottom: 1px solid var(--line);
    }
    .tool-name { font: 650 12px/1 var(--mono); }
    .tool-status { width: max-content; padding: 5px 8px; font: 750 9px/1 var(--mono); letter-spacing: .07em; }
    .tool-status.passed { color: var(--clear); background: #eaf6f1; }
    .tool-status.succeeded { color: var(--clear); background: #eaf6f1; }
    .tool-status.findings { color: var(--high); background: #fbeeea; }
    .tool-status.skipped { color: var(--muted); background: #eef1f5; }
    .tool-status.unavailable, .tool-status.failed, .tool-status.timed_out { color: var(--critical); background: #faeaee; }
    .tool-status.rejected { color: var(--critical); background: #faeaee; }
    .tool-stat { color: #47536a; font: 550 11px/1.4 var(--mono); }
    .tool-detail { color: var(--muted); overflow-wrap: anywhere; font-size: 12px; }

    .intent-card { margin-bottom: 22px; padding: 20px 22px; color: #344056; background: #f5f7ff; border: 1px solid #d8ddf5; border-left: 3px solid var(--indigo); }
    .intent-card h3 { margin: 0; font: 650 18px/1.35 var(--display); }
    .intent-source { display: inline-block; margin-bottom: 10px; color: var(--indigo); font: 700 9px/1 var(--mono); letter-spacing: .09em; text-transform: uppercase; }
    .intent-description { margin: 12px 0 0; white-space: pre-wrap; }
    .intent-meta { display: flex; flex-wrap: wrap; gap: 7px; margin-top: 14px; }
    .intent-meta span { padding: 5px 8px; color: #4d5c82; background: #e8ecfb; font: 600 10px/1 var(--mono); }
    .intent-guidance { margin-top: 15px; }
    .intent-guidance pre { max-height: 240px; overflow: auto; white-space: pre-wrap; }

    .context-overview {
      display: grid;
      grid-template-columns: repeat(6, minmax(0, 1fr));
      border-top: 1px solid var(--ink);
      border-bottom: 1px solid var(--line);
    }
    .context-stat { padding: 18px 16px; border-right: 1px solid var(--line); }
    .context-stat:last-child { border-right: 0; }
    .context-stat span { display: block; color: var(--muted); font: 650 9px/1.2 var(--mono); letter-spacing: .08em; text-transform: uppercase; }
    .context-stat strong { display: block; margin-top: 8px; font: 650 20px/1 var(--display); }
    .context-lead { display: flex; flex-wrap: wrap; align-items: center; gap: 10px 15px; margin: 20px 0 23px; color: var(--muted); }
    .context-state { padding: 5px 8px; color: var(--indigo); background: #eef0fb; font: 750 9px/1 var(--mono); letter-spacing: .08em; }
    .context-state.partial, .context-state.failed { color: var(--critical); background: #faeaee; }
    .symbol-group + .symbol-group { margin-top: 30px; }
    .symbol-group h3 { margin: 0 0 12px; color: var(--muted); font: 700 10px/1 var(--mono); letter-spacing: .11em; text-transform: uppercase; }
    .symbol-grid { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 12px; }
    .symbol-card { min-width: 0; padding: 18px 19px; background: #fbfcfe; border: 1px solid var(--line); }
    .symbol-card.changed { border-top: 3px solid var(--indigo); }
    .symbol-card.related { border-top: 3px solid var(--cyan); }
    .symbol-top { display: flex; align-items: start; justify-content: space-between; gap: 14px; }
    .symbol-name { min-width: 0; overflow-wrap: anywhere; font: 650 14px/1.4 var(--mono); }
    .symbol-kind { flex: none; padding: 4px 7px; color: var(--muted); background: #edf1f6; font: 700 8px/1 var(--mono); letter-spacing: .07em; text-transform: uppercase; }
    .symbol-meta { display: flex; flex-wrap: wrap; gap: 6px 12px; margin-top: 10px; color: var(--muted); font: 500 10px/1.4 var(--mono); }
    .symbol-signature { margin: 13px 0 0; overflow-wrap: anywhere; color: #35425b; font: 550 11px/1.55 var(--mono); }
    .symbol-reasons { margin: 11px 0 0; color: #56637b; font-size: 12px; }
    .symbol-card details { margin-top: 13px; }
    .symbol-detail { display: grid; gap: 13px; padding-top: 14px; }
    .symbol-doc { margin: 0; color: #455169; }
    .symbol-snippet { max-height: 260px; margin: 0; padding: 12px; overflow: auto; white-space: pre; color: #27334a; background: #f1f4f9; font: 500 11px/1.55 var(--mono); }
    .context-warnings { margin: 20px 0 0; padding: 14px 18px 14px 34px; color: #72501c; background: #fff7e8; border: 1px solid #ead8b4; }

    .agent-overview {
      display: grid;
      grid-template-columns: repeat(5, minmax(0, 1fr));
      border-top: 1px solid var(--ink);
      border-bottom: 1px solid var(--line);
    }
    .agent-stat { min-width: 0; padding: 18px 16px; border-right: 1px solid var(--line); }
    .agent-stat:last-child { border-right: 0; }
    .agent-stat span { display: block; color: var(--muted); font: 650 9px/1.2 var(--mono); letter-spacing: .08em; text-transform: uppercase; }
    .agent-stat strong { display: block; margin-top: 8px; overflow-wrap: anywhere; font: 650 16px/1.2 var(--display); }
    .agent-lead { display: flex; flex-wrap: wrap; align-items: center; gap: 10px 15px; margin: 20px 0; color: var(--muted); }
    .agent-state { padding: 5px 8px; color: var(--indigo); background: #eef0fb; font: 750 9px/1 var(--mono); letter-spacing: .08em; }
    .agent-state.partial, .agent-state.failed { color: var(--critical); background: #faeaee; }
    .agent-summary { margin: 0 0 22px; max-width: 860px; color: #3e4960; }
    .unverified-notice { margin: 0 0 22px; padding: 14px 17px; color: #684d1b; background: #fff8e8; border: 1px solid #ead8b4; }
    .agent-tools { margin: 22px 0 26px; border-top: 1px solid var(--ink); }
    .agent-tool-row { display: grid; grid-template-columns: 54px 150px 110px 90px minmax(0, 1fr); gap: 12px; align-items: center; min-height: 54px; border-bottom: 1px solid var(--line); }
    .candidate-list { display: grid; gap: 14px; }
    .candidate {
      --severity: var(--muted);
      padding: 22px 24px;
      background: #fffdf8;
      border: 1px solid #e5dcc6;
      border-left: 3px solid var(--severity);
    }
    .candidate.severity-critical { --severity: var(--critical); }
    .candidate.severity-high { --severity: var(--high); }
    .candidate.severity-medium { --severity: var(--medium); }
    .candidate.severity-low { --severity: var(--low); }
    .candidate.severity-info { --severity: var(--cyan); }
    .candidate-top { display: flex; align-items: start; justify-content: space-between; gap: 20px; }
    .candidate-title { margin: 0; font: 650 17px/1.35 var(--display); letter-spacing: -.015em; }
    .candidate-badges { display: flex; flex-wrap: wrap; justify-content: end; gap: 7px; }
    .unverified-badge { padding: 5px 8px; color: #76551b; border: 1px solid #b58a3f; font: 750 9px/1 var(--mono); letter-spacing: .09em; }
    .verification-list { margin: 0; padding-left: 19px; color: #344056; }
    .agent-warnings { margin: 20px 0 0; padding: 14px 18px 14px 34px; color: #7a3040; background: #fff1f3; border: 1px solid #ebcbd2; }

    .verification-overview {
      display: grid;
      grid-template-columns: repeat(6, minmax(0, 1fr));
      border-top: 1px solid var(--ink);
      border-bottom: 1px solid var(--line);
    }
    .verification-stat { padding: 18px 16px; border-right: 1px solid var(--line); }
    .verification-stat:last-child { border-right: 0; }
    .verification-stat span { display: block; color: var(--muted); font: 650 9px/1.2 var(--mono); letter-spacing: .08em; text-transform: uppercase; }
    .verification-stat strong { display: block; margin-top: 8px; font: 650 20px/1 var(--display); }
    .verification-list { display: grid; gap: 14px; margin-top: 24px; }
    .verification-card { --verdict: var(--muted); padding: 22px 24px; background: #fbfcfe; border: 1px solid var(--line); border-left: 3px solid var(--verdict); }
    .verification-card.verified { --verdict: var(--clear); background: #f5fbf8; }
    .verification-card.rejected { --verdict: var(--critical); background: #fff8f9; }
    .verification-card.inconclusive { --verdict: var(--medium); background: #fffcf5; }
    .verification-card.needs_review { --verdict: var(--medium); background: #fffcf5; }
    .verification-top { display: flex; align-items: start; justify-content: space-between; gap: 18px; }
    .verification-title { margin: 0; font: 650 17px/1.35 var(--display); }
    .verdict-badge { flex: none; padding: 5px 8px; color: var(--verdict); border: 1px solid currentColor; font: 750 9px/1 var(--mono); letter-spacing: .09em; }
    .verification-reason { margin: 15px 0 0; color: #3e4960; }
    .check-list { display: grid; gap: 8px; margin: 0; padding: 0; list-style: none; }
    .check-item { display: grid; grid-template-columns: 78px 150px minmax(0, 1fr); gap: 10px; align-items: start; color: #46526a; font-size: 12px; }
    .check-status { width: max-content; padding: 4px 6px; font: 700 8px/1 var(--mono); letter-spacing: .07em; text-transform: uppercase; }
    .check-status.passed { color: var(--clear); background: #eaf6f1; }
    .check-status.failed { color: var(--critical); background: #faeaee; }
    .check-status.warning { color: #7a571a; background: #fff3d9; }
    .check-name { font: 600 10px/1.4 var(--mono); }
    .source-snapshot { max-height: 260px; margin: 0; padding: 12px; overflow: auto; white-space: pre; color: #27334a; background: #f1f4f9; font: 500 11px/1.55 var(--mono); }
    .verification-warnings { margin: 20px 0 0; padding: 14px 18px 14px 34px; color: #72501c; background: #fff7e8; border: 1px solid #ead8b4; }

    .footer {
      display: flex;
      justify-content: space-between;
      gap: 20px;
      padding: 18px 28px;
      color: var(--muted);
      background: #f8f9fc;
      font: 550 10px/1.4 var(--mono);
      letter-spacing: .04em;
    }

    @keyframes arrive { from { opacity: 0; transform: translateY(8px); } to { opacity: 1; transform: none; } }

    @media (max-width: 760px) {
      .report-shell { width: 100%; margin: 0; border-left: 0; border-right: 0; box-shadow: none; }
      .masthead { padding: 13px 20px; }
      .generated { display: none; }
      .hero { grid-template-columns: 1fr; }
      .hero-copy { padding: 40px 24px 36px; }
      .verdict-panel { min-height: 230px; }
      .metrics { grid-template-columns: repeat(2, 1fr); }
      .metric:nth-child(2) { border-right: 0; }
      .metric:nth-child(-n+2) { border-bottom: 1px solid var(--line); }
      .section { padding: 34px 22px 38px; }
      .section-heading { align-items: start; flex-direction: column; }
      .file-row { grid-template-columns: 88px minmax(0, 1fr) 51px 51px; gap: 8px; padding: 12px 0; }
      .binary { display: none; }
      .tool-row { grid-template-columns: minmax(0, 1fr) auto; gap: 8px 14px; padding: 14px 0; }
      .tool-duration, .tool-suppressed { display: none; }
      .tool-detail { grid-column: 1 / -1; }
      .context-overview { grid-template-columns: repeat(2, 1fr); }
      .context-stat { border-bottom: 1px solid var(--line); }
      .agent-overview { grid-template-columns: repeat(2, 1fr); }
      .agent-stat { border-bottom: 1px solid var(--line); }
      .agent-tool-row { grid-template-columns: 44px minmax(0, 1fr) auto; padding: 12px 0; }
      .agent-tool-result { grid-column: 1 / -1; }
      .candidate-top { flex-direction: column-reverse; gap: 10px; }
      .candidate-badges { justify-content: start; }
      .verification-overview { grid-template-columns: repeat(2, 1fr); }
      .verification-stat { border-bottom: 1px solid var(--line); }
      .verification-top { flex-direction: column-reverse; gap: 10px; }
      .check-item { grid-template-columns: 72px minmax(0, 1fr); }
      .check-item span:last-child { grid-column: 1 / -1; }
      .symbol-grid { grid-template-columns: 1fr; }
      .risk-spine { padding-left: 24px; }
      .risk-spine::before { left: 5px; }
      .finding::before { left: -25px; }
      .finding { padding: 19px 18px; }
      .finding-top { flex-direction: column-reverse; gap: 10px; }
      .detail-grid { grid-template-columns: 1fr; }
      .footer { flex-direction: column; }
    }

    @media (prefers-reduced-motion: reduce) { .report-shell { animation: none; } }

    @media print {
      body { background: white; }
      .report-shell { width: 100%; margin: 0; border: 0; box-shadow: none; }
      .verdict-panel { print-color-adjust: exact; -webkit-print-color-adjust: exact; }
      details > * { display: block; }
    }

    {{reportCSS}}
  </style>
</head>
<body id="top">
  <a class="skip-link" href="#findings-heading">Skip to findings</a>
  <main class="report-shell">
    <header class="masthead">
      <div class="brand">
        <svg class="brand-mark" viewBox="0 0 32 36" aria-hidden="true">
          <path d="M16 1.8 29 6.7v9.9c0 8.2-5.2 14.6-13 17.6C8.2 31.2 3 24.8 3 16.6V6.7L16 1.8Z" fill="none" stroke="currentColor" stroke-width="2"/>
          <path d="m10.1 18.4 3.8 3.8 8.5-9" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="square"/>
        </svg>
        <div><span class="brand-name">AegisCodeAgent</span><span class="brand-kind">Evidence review dossier</span></div>
      </div>
      <div class="generated">{{formatTime .GeneratedAt}}</div>
    </header>

    <nav class="report-nav" aria-label="Report sections">
      <a href="#decision-heading">Decision</a>
      <a href="#findings-heading"><span class="nav-alert" aria-hidden="true"></span>Findings <b>{{len .Findings}}</b></a>
      {{if ne .Verification.Status "not_run"}}<a href="#verification-heading">Verification</a>{{end}}
      {{if .Analysis.Tools}}<a href="#analyzers-heading">Analyzers</a>{{end}}
      {{if ne .Agent.Status "not_run"}}<a href="#agent-heading">Agent trace</a>{{end}}
      {{if ne .Context.Status "not_run"}}<a href="#context-heading">Context</a>{{end}}
      <a href="#files-heading">Files</a>
    </nav>

    <section class="hero">
      <div class="hero-copy">
        <p class="eyebrow">Pull request evidence · Schema {{.SchemaVersion}}</p>
        <h1>{{.Verdict.Title}}</h1>
        <p class="hero-summary">{{.Verdict.Summary}}</p>
        <p class="repository"><span>Repository workspace</span>{{.Comparison.Repository}}</p>
        <div class="commits">
          <code class="commit">base {{shortCommit .Comparison.BaseCommit}}</code>
          <span class="comparison-arrow" aria-hidden="true">→</span>
          <code class="commit">head {{shortCommit .Comparison.HeadCommit}}</code>
        </div>
      </div>
      <aside class="verdict-panel risk-{{.Verdict.Class}}" aria-label="Review verdict: {{.Verdict.Title}}">
        <div class="verdict">
          <span class="verdict-label">{{.Verdict.Label}}</span>
          <span class="verdict-count">{{.Verdict.Count}}</span>
        </div>
        <p class="verdict-caption">{{.Verdict.Caption}}</p>
      </aside>
    </section>

    <section class="metrics" aria-label="Review summary">
      <div class="metric"><span class="metric-label">Changed files</span><span class="metric-value">{{.Summary.ChangedFiles}}</span></div>
      <div class="metric"><span class="metric-label">Line movement</span><span class="metric-value"><span class="plus">+{{.Summary.Additions}}</span> <span class="minus">−{{.Summary.Deletions}}</span></span></div>
      <div class="metric"><span class="metric-label">P0 / P1</span><span class="metric-value metric-clear">{{.Counts.P0}} / {{.Counts.P1}}</span></div>
      <div class="metric"><span class="metric-label">P2 / P3</span><span class="metric-value metric-review">{{.Counts.P2}} / {{.Counts.P3}}</span></div>
    </section>

    <section class="decision-section" aria-labelledby="decision-heading">
      <div class="decision-copy">
        <span class="decision-kicker">Merge decision trace</span>
        <h2 id="decision-heading">{{.Verdict.DecisionTitle}}</h2>
        <p>{{.Verdict.DecisionMessage}}</p>
        <div class="decision-notes">
          <span><b>{{if .Gate.Blocked}}Blocked{{else}}Passed{{end}}</b> merge gate</span>
          <span><b>{{.Counts.P0}}</b> P0 · <b>{{.Counts.P1}}</b> P1</span>
          <span><b>{{.Counts.P2}}</b> P2 · <b>{{.Counts.P3}}</b> P3</span>
          <span>threshold <b>{{priorityLabel .Options.FailOn}}</b></span>
        </div>
        {{if .Gate.IncompleteReasons}}<ul class="decision-reasons">{{range .Gate.IncompleteReasons}}<li>{{.}}</li>{{end}}</ul>{{end}}
        {{if .Gate.DegradedReasons}}<ul class="decision-reasons degraded">{{range .Gate.DegradedReasons}}<li>{{.}}</li>{{end}}</ul>{{end}}
      </div>
      <ol class="stage-track" aria-label="Review pipeline status">
        <li class="stage-complete"><span>01</span><div><b>Diff</b><small>Complete · {{.Summary.ChangedFiles}} files</small></div></li>
        <li class="stage-{{.Analysis.Status}}"><span>02</span><div><b>Static analysis</b><small>{{analysisLabel .Analysis}} · {{len .Analysis.Tools}} tools</small></div></li>
        <li class="stage-{{.Context.Status}}"><span>03</span><div><b>Repository context</b><small>{{contextLabel .Context.Status}} · {{.Context.Stats.SymbolsSelected}} symbols</small></div></li>
        <li class="stage-{{.Agent.Status}}"><span>04</span><div><b>Reasoning agent</b><small>{{agentLabel .Agent.Status}} · {{len .Agent.Candidates}} candidates</small></div></li>
        <li class="stage-{{.Verification.Status}}"><span>05</span><div><b>Verifier</b><small>{{verificationLabel .Verification.Status}} · {{.Verification.Summary.Promoted}} promoted</small></div></li>
      </ol>
    </section>

    <section class="section findings-section" aria-labelledby="findings-heading">
      <div class="section-heading">
        <h2 id="findings-heading">Review findings</h2>
        <p class="section-note">Evidence ranked consistently from P0 to P3</p>
      </div>
      {{if .Findings}}
      <div class="risk-spine">
        {{range .Findings}}
        <article class="finding severity-{{.Severity}}">
          <div class="finding-top">
            <h3>{{.Title}}</h3>
            <span class="severity">{{severityLabel .Severity}}</span>
          </div>
          <div class="finding-meta">
            <span>{{.Category}}</span>
            <span>{{location .Location}}</span>
            <span>{{confidence .Confidence}} confidence</span>
            {{if .Source}}<span>{{.Source}}</span>{{end}}
          </div>
          {{if .Description}}<p class="finding-description">{{.Description}}</p>{{end}}
          {{if hasDetails .}}
          <details>
            <summary>Inspect evidence and recommendation</summary>
            <div class="detail-grid">
              {{if .Evidence}}<div class="detail-block"><h4>Evidence</h4><pre>{{.Evidence}}</pre></div>{{end}}
              {{if .Suggestion}}<div class="detail-block"><h4>Recommendation</h4><p>{{.Suggestion}}</p></div>{{end}}
            </div>
          </details>
          {{end}}
        </article>
        {{end}}
      </div>
      {{else}}
      <div class="empty-state">
        <svg class="empty-icon" viewBox="0 0 44 50" aria-hidden="true"><path d="M22 2 40 9v13.4c0 11-7.2 19.7-18 23.6C11.2 42.1 4 33.4 4 22.4V9l18-7Z" fill="none" stroke="currentColor" stroke-width="2"/><path d="m14 25 5 5 11-12" fill="none" stroke="currentColor" stroke-width="2.4"/></svg>
        <div><strong>{{emptyTitle .ReviewReport}}</strong><p>{{emptyMessage .ReviewReport}}</p></div>
      </div>
      {{end}}
    </section>

    <section class="section" aria-labelledby="files-heading">
      <div class="section-heading">
        <h2 id="files-heading">Change surface</h2>
        <p class="section-note">Files touched by this comparison</p>
      </div>
      {{if .Files}}
      <details class="section-disclosure file-disclosure">
        <summary><span><b>{{.Summary.ChangedFiles}} changed files</b><small>+{{.Summary.Additions}} / −{{.Summary.Deletions}} lines</small></span><em>Inspect change surface</em></summary>
      <div class="file-table">
        {{range .Files}}
        <div class="file-row">
          <span class="status">{{statusLabel .Status}}</span>
          <code class="file-path">{{.Path}}</code>
          <span class="delta add">+{{.Stats.Additions}}</span>
          <span class="delta remove">−{{.Stats.Deletions}}</span>
          <span class="binary">{{if .Binary}}binary{{else}}text{{end}}</span>
        </div>
        {{end}}
      </div>
      </details>
      {{else}}
      <div class="no-files">No changed files were found for this comparison.</div>
      {{end}}
    </section>

    {{if .Analysis.Tools}}
    <section class="section" aria-labelledby="analyzers-heading">
      <div class="section-heading">
        <h2 id="analyzers-heading">Analyzer execution</h2>
        <p class="section-note">Deterministic tools used for this review</p>
      </div>
      <div class="tool-table">
        {{range .Analysis.Tools}}
        <div class="tool-row">
          <code class="tool-name">{{.Name}}</code>
          <span class="tool-status {{.Status}}">{{toolLabel .Status}}</span>
          <span class="tool-stat tool-duration">{{duration .DurationMillis}}</span>
          <span class="tool-stat">{{.Findings}} findings</span>
          <span class="tool-stat tool-suppressed">{{.Suppressed}} suppressed</span>
          <span class="tool-detail">{{.Detail}}</span>
        </div>
        {{end}}
      </div>
    </section>
    {{end}}

    {{if ne .Context.Status "not_run"}}
    <section class="section" aria-labelledby="context-heading">
      <div class="section-heading">
        <h2 id="context-heading">Repository context</h2>
        <p class="section-note">Change intent and AST-derived evidence selected for agent reasoning</p>
      </div>
      {{if .Context.Intent.Source}}
      <article class="intent-card">
        <span class="intent-source">Change intent · {{.Context.Intent.Source}}</span>
        {{if .Context.Intent.Title}}<h3>{{.Context.Intent.Title}}</h3>{{end}}
        {{if .Context.Intent.Description}}<p class="intent-description">{{.Context.Intent.Description}}</p>{{end}}
        <div class="intent-meta">
          {{range .Context.Intent.Labels}}<span>label: {{.}}</span>{{end}}
          {{range .Context.Intent.LinkedIssues}}<span>issue: {{.}}</span>{{end}}
          {{if .Context.Intent.Truncated}}<span>payload truncated</span>{{end}}
        </div>
        {{if .Context.Intent.RepositoryGuidance}}
        <div class="intent-guidance">
          {{range .Context.Intent.RepositoryGuidance}}<details><summary>Repository guidance · {{.Path}}</summary><pre>{{.Content}}</pre></details>{{end}}
        </div>
        {{end}}
      </article>
      {{end}}
      <div class="context-overview">
        <div class="context-stat"><span>Packages</span><strong>{{.Context.Stats.PackagesLoaded}}</strong></div>
        <div class="context-stat"><span>Type checked</span><strong>{{.Context.Stats.PackagesTypeChecked}}</strong></div>
        <div class="context-stat"><span>Files parsed</span><strong>{{.Context.Stats.FilesParsed}}</strong></div>
        <div class="context-stat"><span>Symbols indexed</span><strong>{{.Context.Stats.SymbolsIndexed}}</strong></div>
        <div class="context-stat"><span>Relations</span><strong>{{.Context.Stats.RelationsIndexed}}</strong></div>
        <div class="context-stat"><span>Token estimate</span><strong>{{.Context.Stats.EstimatedTokens}}</strong></div>
      </div>
      <div class="context-lead">
        <span class="context-state {{.Context.Status}}">{{contextLabel .Context.Status}}</span>
        <span>{{.Context.Stats.SymbolsSelected}} symbols selected{{if .Context.Truncated}} · budget reached{{end}}</span>
      </div>
      {{if or .Context.ChangedSymbols .Context.RelatedSymbols}}
      <details class="section-disclosure context-disclosure">
        <summary><span><b>{{.Context.Stats.SymbolsSelected}} selected symbols</b><small>AST, type and relationship evidence used for reasoning</small></span><em>Inspect repository context</em></summary>
      {{if .Context.ChangedSymbols}}
      <div class="symbol-group">
        <h3>Changed symbols</h3>
        <div class="symbol-grid">
          {{range .Context.ChangedSymbols}}
          <article class="symbol-card changed">
            <div class="symbol-top"><code class="symbol-name">{{.QualifiedName}}</code><span class="symbol-kind">{{.Kind}}</span></div>
            <div class="symbol-meta"><span>{{symbolLocation .}}</span><span>{{.Package}}</span></div>
            {{if .Signature}}<p class="symbol-signature">{{.Signature}}</p>{{end}}
            {{if hasSymbolDetail .}}<details><summary>Inspect symbol context</summary><div class="symbol-detail">{{if .Documentation}}<p class="symbol-doc">{{.Documentation}}</p>{{end}}{{if .Snippet}}<pre class="symbol-snippet">{{.Snippet}}</pre>{{end}}</div></details>{{end}}
          </article>
          {{end}}
        </div>
      </div>
      {{end}}
      {{if .Context.RelatedSymbols}}
      <div class="symbol-group">
        <h3>Related symbols</h3>
        <div class="symbol-grid">
          {{range .Context.RelatedSymbols}}
          <article class="symbol-card related">
            <div class="symbol-top"><code class="symbol-name">{{.QualifiedName}}</code><span class="symbol-kind">{{.Kind}}</span></div>
            <div class="symbol-meta"><span>{{symbolLocation .}}</span><span>score {{.RelevanceScore}}</span></div>
            {{if .Signature}}<p class="symbol-signature">{{.Signature}}</p>{{end}}
            {{if .Reasons}}<p class="symbol-reasons">{{join .Reasons " · "}}</p>{{end}}
            {{if hasSymbolDetail .}}<details><summary>Inspect symbol context</summary><div class="symbol-detail">{{if .Documentation}}<p class="symbol-doc">{{.Documentation}}</p>{{end}}{{if .Snippet}}<pre class="symbol-snippet">{{.Snippet}}</pre>{{end}}</div></details>{{end}}
          </article>
          {{end}}
        </div>
      </div>
      {{end}}
      </details>
      {{end}}
      {{if .Context.Warnings}}<ul class="context-warnings">{{range .Context.Warnings}}<li>{{.}}</li>{{end}}</ul>{{end}}
    </section>
    {{end}}

    {{if ne .Agent.Status "not_run"}}
    <section class="section" aria-labelledby="agent-heading">
      <div class="section-heading">
        <h2 id="agent-heading">Reasoning agent</h2>
        <p class="section-note">Bounded model reasoning with a read-only tool audit</p>
      </div>
      <div class="agent-overview">
        <div class="agent-stat"><span>Provider</span><strong>{{.Agent.Provider}}</strong></div>
        <div class="agent-stat"><span>Model</span><strong>{{.Agent.Model}}</strong></div>
        <div class="agent-stat"><span>Turns</span><strong>{{.Agent.Steps}}</strong></div>
        <div class="agent-stat"><span>Total tokens</span><strong>{{.Agent.Usage.TotalTokens}}</strong></div>
        <div class="agent-stat"><span>Candidates</span><strong>{{len .Agent.Candidates}}</strong></div>
      </div>
      <div class="agent-lead">
        <span class="agent-state {{.Agent.Status}}">{{agentLabel .Agent.Status}}</span>
        <span>{{duration .Agent.DurationMillis}} · thinking {{if .Agent.Thinking}}enabled{{else}}disabled{{end}}</span>
        <span>{{.Agent.Usage.PromptTokens}} prompt · {{.Agent.Usage.CompletionTokens}} completion tokens</span>
      </div>
      <p class="unverified-notice"><strong>Verification boundary:</strong> model candidates are hypotheses and do not affect the verdict above until a deterministic verifier accepts them.</p>
      {{if .Agent.Summary}}<p class="agent-summary">{{.Agent.Summary}}</p>{{end}}
      {{if .Agent.ToolCalls}}
      <details class="section-disclosure audit-disclosure">
        <summary><span><b>{{len .Agent.ToolCalls}} audited tool calls</b><small>Read-only code retrieval performed by the reasoning agent</small></span><em>Inspect agent trace</em></summary>
      <div class="agent-tools" aria-label="Agent tool audit">
        {{range .Agent.ToolCalls}}
        <div class="agent-tool-row">
          <span class="tool-stat">#{{.Step}}</span>
          <code class="tool-name">{{.Name}}</code>
          <span class="tool-status {{.Status}}">{{.Status}}</span>
          <span class="tool-stat">{{duration .DurationMillis}}</span>
          <span class="tool-detail agent-tool-result">{{.ResultSummary}}</span>
        </div>
        {{end}}
      </div>
      </details>
      {{end}}
      {{if .Agent.Candidates}}
      <div class="candidate-list">
        {{range .Agent.Candidates}}
        <article class="candidate severity-{{.Severity}}">
          <div class="candidate-top">
            <h3 class="candidate-title">{{.Title}}</h3>
            <div class="candidate-badges"><span class="unverified-badge">AGENT CANDIDATE</span><span class="severity">{{severityLabel .Severity}}</span></div>
          </div>
          <div class="finding-meta">
            <span>{{.Category}}</span>
            <span>{{location .Location}}</span>
            <span>{{confidence .Confidence}} model confidence</span>
            <span>{{.ID}}</span>
          </div>
          <p class="finding-description">{{.Description}}</p>
          {{if hasCandidateDetails .}}
          <details>
            <summary>Inspect candidate evidence and verification plan</summary>
            <div class="detail-grid">
              <div class="detail-block"><h4>Evidence</h4><pre>{{.Evidence}}</pre></div>
              <div class="detail-block"><h4>Suggested correction</h4><p>{{.Suggestion}}</p></div>
              <div class="detail-block"><h4>Verification plan</h4><ul class="verification-list">{{range .Verification}}<li>{{.}}</li>{{end}}</ul></div>
            </div>
          </details>
          {{end}}
        </article>
        {{end}}
      </div>
      {{end}}
      {{if .Agent.Warnings}}<ul class="agent-warnings">{{range .Agent.Warnings}}<li>{{.}}</li>{{end}}</ul>{{end}}
    </section>
    {{end}}

    {{if ne .Verification.Status "not_run"}}
    <section class="section" aria-labelledby="verification-heading">
      <div class="section-heading">
        <h2 id="verification-heading">Verification</h2>
        <p class="section-note">Deterministic evidence gate for Agent candidates</p>
      </div>
      <div class="verification-overview">
        <div class="verification-stat"><span>Status</span><strong>{{verificationLabel .Verification.Status}}</strong></div>
        <div class="verification-stat"><span>Verified</span><strong>{{.Verification.Summary.Verified}}</strong></div>
        <div class="verification-stat"><span>Needs review</span><strong>{{needsReviewCount .Verification}}</strong></div>
        <div class="verification-stat"><span>Rejected</span><strong>{{.Verification.Summary.Rejected}}</strong></div>
        <div class="verification-stat"><span>Semantic evidence</span><strong>{{.Verification.Summary.SemanticFindings}}</strong></div>
        <div class="verification-stat"><span>Promoted</span><strong>{{.Verification.Summary.Promoted}}</strong></div>
      </div>
      <div class="agent-lead">
        <span>{{duration .Verification.DurationMillis}}</span>
        <span>{{.Verification.Summary.Candidates}} candidate(s) adjudicated</span>
        <span>Only independently evidenced findings enter the final verdict</span>
      </div>
      {{if .Verification.Tools}}
      <div class="tool-table" aria-label="Focused verification tools">
        {{range .Verification.Tools}}
        <div class="tool-row">
          <code class="tool-name">{{.Name}}</code>
          <span class="tool-status {{.Status}}">{{toolLabel .Status}}</span>
          <span class="tool-stat tool-duration">{{duration .DurationMillis}}</span>
          <span class="tool-stat">{{.Findings}} findings</span>
          <span class="tool-stat tool-suppressed">{{.Suppressed}} suppressed</span>
          <span class="tool-detail">{{.Detail}}</span>
        </div>
        {{end}}
      </div>
      {{end}}
      {{if .Verification.Candidates}}
      <div class="verification-list">
        {{range .Verification.Candidates}}
        <article class="verification-card {{.Verdict}}">
          <div class="verification-top">
            <h3 class="verification-title">{{.Title}}</h3>
            <span class="verdict-badge">{{verdictLabel .Verdict}}</span>
          </div>
          <div class="finding-meta">
            <span>{{location .Location}}</span>
            <span>{{confidence .CalibratedConfidence}} calibrated confidence</span>
            <span>{{.CandidateID}}</span>
            {{if .FindingID}}<span>{{if .Promoted}}promoted{{else}}linked{{end}} → {{.FindingID}}</span>{{end}}
          </div>
          <p class="verification-reason">{{.Reason}}</p>
          <details>
            <summary>Inspect verification evidence</summary>
            <div class="detail-grid">
              {{if .SourceSnapshot}}<div class="detail-block"><h4>Exact source snapshot</h4><pre class="source-snapshot">{{.SourceSnapshot}}</pre></div>{{end}}
              <div class="detail-block"><h4>Checks</h4><ul class="check-list">{{range .Checks}}<li class="check-item"><span class="check-status {{.Status}}">{{.Status}}</span><span class="check-name">{{.Name}}</span><span>{{.Detail}}</span></li>{{end}}</ul></div>
              {{if .MatchedFindingIDs}}<div class="detail-block"><h4>Corroborating findings</h4><p>{{join .MatchedFindingIDs " · "}}</p></div>{{end}}
            </div>
          </details>
        </article>
        {{end}}
      </div>
      {{end}}
      {{if .Verification.Warnings}}<ul class="verification-warnings">{{range .Verification.Warnings}}<li>{{.}}</li>{{end}}</ul>{{end}}
    </section>
    {{end}}

    <footer class="footer"><span>Generated by AegisCodeAgent · {{formatTime .GeneratedAt}}</span><a href="#top">Back to top ↑</a><span>Schema {{.SchemaVersion}} · Evidence-driven review</span></footer>
  </main>
</body>
</html>
`
