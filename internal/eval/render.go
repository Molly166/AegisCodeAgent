package eval

import (
	"bytes"
	"encoding/json"
	"fmt"
	"html/template"
	"strings"

	"github.com/Molly166/AegisCodeAgent/internal/review"
)

const (
	FormatHTML = "html"
	FormatJSON = "json"
)

func Render(format string, report HarnessReport) ([]byte, error) {
	switch strings.ToLower(strings.TrimSpace(format)) {
	case FormatJSON:
		encoded, err := json.MarshalIndent(report, "", "  ")
		if err != nil {
			return nil, fmt.Errorf("render eval JSON: %w", err)
		}
		return append(encoded, '\n'), nil
	case FormatHTML:
		var output bytes.Buffer
		if err := evalHTMLTemplate.Execute(&output, report); err != nil {
			return nil, fmt.Errorf("render eval HTML: %w", err)
		}
		return output.Bytes(), nil
	default:
		return nil, fmt.Errorf("unsupported eval format %q (supported: html, json)", format)
	}
}

var evalHTMLTemplate = template.Must(template.New("aegis-eval").Funcs(template.FuncMap{
	"percent": func(value float64) string { return fmt.Sprintf("%.1f%%", value*100) },
	"caseClass": func(passed bool) string {
		if passed {
			return "pass"
		}
		return "fail"
	},
	"caseLabel": func(passed bool) string {
		if passed {
			return "PASS"
		}
		return "FAIL"
	},
	"location": func(finding review.Finding) string {
		if finding.Location.StartLine > 0 {
			return fmt.Sprintf("%s:%d", finding.Location.Path, finding.Location.StartLine)
		}
		return finding.Location.Path
	},
	"expectedLocation": func(finding ExpectedFinding) string {
		if finding.StartLine > 0 {
			return fmt.Sprintf("%s:%d", finding.Path, finding.StartLine)
		}
		return finding.Path
	},
}).Parse(evalHTMLSource))

const evalHTMLSource = `<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>Aegis Eval · {{.GeneratedAt.Format "2006-01-02"}}</title>
  <style>
    :root { --ink:#162036; --muted:#68748b; --line:#dce2ec; --paper:#f1f4f8; --surface:#fff; --indigo:#4458b7; --green:#1f7a58; --red:#b32645; --amber:#b57515; --mono:"SFMono-Regular","Cascadia Code",monospace; --sans:"Avenir Next","Segoe UI",sans-serif; }
    * { box-sizing:border-box; }
    body { margin:0; color:var(--ink); background:linear-gradient(120deg,#eef1f8,#f7f8fb 55%,#edf5f3); font:14px/1.55 var(--sans); }
    main { width:min(1180px,calc(100% - 36px)); margin:32px auto 60px; background:var(--surface); border:1px solid var(--line); box-shadow:0 24px 70px rgba(28,40,70,.10); }
    header { display:grid; grid-template-columns:minmax(0,1fr) 250px; border-bottom:1px solid var(--line); }
    .hero { padding:48px 52px; }
    .eyebrow,.label { color:var(--indigo); font:750 10px/1 var(--mono); letter-spacing:.12em; text-transform:uppercase; }
    h1 { margin:16px 0 8px; font-size:42px; line-height:1.08; letter-spacing:-.035em; }
    .subtitle { max-width:680px; margin:0; color:var(--muted); font-size:16px; }
    .score { display:flex; flex-direction:column; justify-content:center; padding:34px; color:#fff; background:var(--ink); }
    .score strong { margin-top:12px; font-size:48px; line-height:1; }
    .score span:last-child { margin-top:10px; color:#bdc6d9; }
    section { padding:34px 42px; border-bottom:1px solid var(--line); }
    h2 { margin:0 0 20px; font-size:22px; }
    .metrics { display:grid; grid-template-columns:repeat(6,minmax(0,1fr)); border-top:1px solid var(--ink); border-bottom:1px solid var(--line); }
    .metric { min-width:0; padding:17px 15px; border-right:1px solid var(--line); }
    .metric:last-child { border-right:0; }
    .metric span { display:block; color:var(--muted); font:700 9px/1.2 var(--mono); letter-spacing:.08em; text-transform:uppercase; }
    .metric strong { display:block; margin-top:9px; font-size:21px; }
    .gate { margin-top:18px; color:var(--muted); font-family:var(--mono); font-size:11px; }
    .cases { display:grid; gap:14px; }
    article { border:1px solid var(--line); border-left:4px solid var(--red); background:#fffafb; }
    article.pass { border-left-color:var(--green); background:#f8fcfa; }
    .case-head { display:flex; align-items:flex-start; justify-content:space-between; gap:20px; padding:20px 22px; }
    .case-head h3 { margin:5px 0 0; font-size:18px; }
    .badge { padding:6px 9px; color:var(--red); border:1px solid currentColor; font:800 9px/1 var(--mono); letter-spacing:.1em; }
    article.pass .badge { color:var(--green); }
    .case-meta { display:flex; flex-wrap:wrap; gap:7px 14px; margin-top:9px; color:var(--muted); font:550 10px/1.3 var(--mono); }
    .case-body { padding:0 22px 22px; }
    .case-body p { color:#46536a; }
    details { margin-top:12px; border-top:1px solid var(--line); }
    summary { padding:13px 0; cursor:pointer; font-weight:650; }
    table { width:100%; border-collapse:collapse; }
    th,td { padding:10px 9px; text-align:left; vertical-align:top; border-top:1px solid var(--line); }
    th { color:var(--muted); font:700 9px/1.2 var(--mono); letter-spacing:.08em; text-transform:uppercase; }
    code { font-family:var(--mono); font-size:11px; }
    .ok { color:var(--green); } .bad { color:var(--red); } .warn { color:var(--amber); }
    footer { display:flex; justify-content:space-between; padding:18px 32px; color:var(--muted); font:500 10px/1.3 var(--mono); }
    @media(max-width:850px){ header{grid-template-columns:1fr}.metrics{grid-template-columns:repeat(2,1fr)}.metric:nth-child(2n){border-right:0}section{padding:28px 22px}.hero{padding:38px 28px}h1{font-size:34px} }
  </style>
</head>
<body>
<main>
  <header>
    <div class="hero"><span class="eyebrow">AegisCodeAgent · Eval Harness</span><h1>Review quality, measured.</h1><p class="subtitle">Replay evaluation of finding recall, precision, merge-gate behavior, unresolved hypotheses, and clean-change false blocks.</p></div>
    <div class="score"><span class="label">Cases passing</span><strong>{{.Metrics.PassedCases}} / {{.Metrics.Cases}}</strong><span>{{percent .Metrics.GateAccuracy}} gate accuracy</span></div>
  </header>
  <section>
    <h2>Quality metrics</h2>
    <div class="metrics">
      <div class="metric"><span>Precision</span><strong>{{percent .Metrics.Precision}}</strong></div>
      <div class="metric"><span>Recall</span><strong>{{percent .Metrics.Recall}}</strong></div>
      <div class="metric"><span>F1</span><strong>{{percent .Metrics.F1}}</strong></div>
      <div class="metric"><span>P0 recall</span><strong>{{percent .Metrics.P0Recall}}</strong></div>
      <div class="metric"><span>P1 recall</span><strong>{{percent .Metrics.P1Recall}}</strong></div>
      <div class="metric"><span>False block rate</span><strong>{{percent .Metrics.FalseBlockRate}}</strong></div>
    </div>
    <div class="metrics" style="margin-top:18px">
      <div class="metric"><span>Matched</span><strong>{{.Metrics.MatchedFindings}}</strong></div>
      <div class="metric"><span>False positives</span><strong>{{.Metrics.FalsePositives}}</strong></div>
      <div class="metric"><span>False negatives</span><strong>{{.Metrics.FalseNegatives}}</strong></div>
      <div class="metric"><span>Needs review</span><strong>{{.Metrics.UnresolvedHypotheses}}</strong></div>
      <div class="metric"><span>Agent tokens</span><strong>{{.Metrics.AgentTokens}}</strong></div>
      <div class="metric"><span>Agent duration</span><strong>{{.Metrics.AgentDurationMillis}} ms</strong></div>
    </div>
    <p class="gate">Gate configuration · findings={{.Gate.FailOn}} · needs-review={{.Gate.FailOnNeedsReview}} · fail-on-incomplete={{.Gate.FailOnIncomplete}}</p>
  </section>
  <section>
    <h2>Corpus results</h2>
    <div class="cases">
    {{range .Cases}}
      <article class="{{caseClass .Passed}}">
        <div class="case-head"><div><span class="label">{{.ID}}</span><h3>{{.Title}}</h3><div class="case-meta"><span>gate {{.ExpectedGate}} → {{.ActualGate}}</span><span>{{len .Matches}} matched</span><span>{{len .Missed}} missed</span><span>{{len .Unexpected}} unexpected</span><span>needs review {{.ExpectedNeedsReview}} → {{.NeedsReview}}</span></div></div><span class="badge">{{caseLabel .Passed}}</span></div>
        <div class="case-body">
          {{if .Description}}<p>{{.Description}}</p>{{end}}
          {{if .Matches}}<details><summary class="ok">Matched findings</summary><table><thead><tr><th>Severity</th><th>Location</th><th>Finding</th><th>Rule</th></tr></thead><tbody>{{range .Matches}}<tr><td>{{.Actual.Severity}}</td><td><code>{{location .Actual}}</code></td><td>{{.Actual.Title}}</td><td><code>{{.Actual.RuleID}}</code></td></tr>{{end}}</tbody></table></details>{{end}}
          {{if .Missed}}<details open><summary class="bad">Missed expectations</summary><table><thead><tr><th>Severity</th><th>Location</th><th>Expected title</th><th>Rule</th></tr></thead><tbody>{{range .Missed}}<tr><td>{{.Severity}}</td><td><code>{{expectedLocation .}}</code></td><td>{{.TitleContains}}</td><td><code>{{.RuleID}}</code></td></tr>{{end}}</tbody></table></details>{{end}}
          {{if .Unexpected}}<details open><summary class="warn">Unexpected findings</summary><table><thead><tr><th>Severity</th><th>Location</th><th>Finding</th><th>Source</th></tr></thead><tbody>{{range .Unexpected}}<tr><td>{{.Severity}}</td><td><code>{{location .}}</code></td><td>{{.Title}}</td><td><code>{{.Source}}</code></td></tr>{{end}}</tbody></table></details>{{end}}
        </div>
      </article>
    {{end}}
    </div>
  </section>
  <footer><span>Generated {{.GeneratedAt.Format "2006-01-02 15:04 UTC"}}</span><span>Schema {{.SchemaVersion}} · {{.Corpus}}</span></footer>
</main>
</body>
</html>`
