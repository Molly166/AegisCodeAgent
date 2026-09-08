package liveeval

import (
	"bytes"
	"encoding/json"
	"fmt"
	"html/template"
)

func Render(format string, report Report) ([]byte, error) {
	switch format {
	case "json":
		data, err := json.MarshalIndent(report, "", "  ")
		return append(data, '\n'), err
	case "html":
		var output bytes.Buffer
		if err := liveTemplate.Execute(&output, report); err != nil {
			return nil, err
		}
		return output.Bytes(), nil
	default:
		return nil, fmt.Errorf("unsupported live eval format %q (html or json)", format)
	}
}

var liveTemplate = template.Must(template.New("live-eval").Funcs(template.FuncMap{
	"rate": func(r Rate) string {
		if r.Value == nil {
			return "N/A (0 support)"
		}
		return fmt.Sprintf("%.1f%% (%d/%d)", *r.Value*100, r.Numerator, r.Denominator)
	},
	"json": func(v any) string { b, _ := json.MarshalIndent(v, "", "  "); return string(b) },
}).Parse(`<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>Aegis · Live pipeline evaluation</title><style>
:root{color-scheme:light;--ink:#182435;--muted:#526274;--line:#dce2e8}*{box-sizing:border-box}body{margin:0;background:#f4f6f8;color:var(--ink);font:15px/1.55 system-ui,sans-serif}main{max-width:1100px;margin:32px auto;padding:32px;background:#fff;border:1px solid var(--line)}h1{font-size:30px;line-height:1.2}h2{margin-top:30px}p{color:var(--muted)}.banner{padding:18px;border-left:4px solid #375a7a;background:#edf3f7}.grid{display:grid;grid-template-columns:repeat(auto-fit,minmax(230px,1fr));gap:12px}.metric{padding:16px;border:1px solid var(--line)}.metric strong{display:block;font-size:19px}table{width:100%;border-collapse:collapse}td,th{padding:10px;text-align:left;vertical-align:top;border-bottom:1px solid var(--line)}th{font-size:12px;text-transform:uppercase}.scroll{overflow:auto}.pass{color:#126140}.fail{color:#ad2836}details{margin-top:14px;border:1px solid var(--line);padding:14px}summary{cursor:pointer;font-weight:650}pre{white-space:pre-wrap;overflow-wrap:anywhere;font:12px/1.5 ui-monospace,monospace}code{overflow-wrap:anywhere}footer{margin-top:28px;font-size:12px;color:var(--muted)}@media(max-width:600px){main{margin:0;padding:18px}h1{font-size:25px}}@media print{details{break-inside:avoid}}
</style></head><body><main><p>AegisCodeAgent · {{.Mode}} · {{.GeneratedAt.Format "2006-01-02 15:04 UTC"}}</p><h1>Live pipeline evaluation</h1>
<div class="banner"><strong>{{.Metrics.PassedRuns}} / {{.Metrics.Runs}} runs met every expectation</strong><br>{{if .LiveModel}}Model requested: {{.Provider}} / {{.RequestedModel}}. Completed model runs: {{rate .Metrics.AgentCompletionRate}}.{{else}}Deterministic baseline; no model API was called. These are not model-quality measurements.{{end}}<br>{{.Metrics.UniqueCases}} synthetic cases × {{.Repeats}} repeat(s). Analyze misses before making accuracy claims.</div>
<h2>Observed metrics</h2><div class="grid"><div class="metric">Finding precision<strong>{{rate .Metrics.Precision}}</strong></div><div class="metric">Finding recall<strong>{{rate .Metrics.Recall}}</strong></div><div class="metric">Complete and correct gate<strong>{{rate .Metrics.GateAccuracy}}</strong></div><div class="metric">Clean false-block rate<strong>{{rate .Metrics.FalseBlockRate}}</strong></div><div class="metric">Incomplete rate<strong>{{rate .Metrics.IncompleteRate}}</strong></div><div class="metric">Repeated gate consistency<strong>{{rate .Metrics.GateConsistency}}</strong></div>{{range $key, $value := .Metrics.PriorityRecall}}<div class="metric">{{$key}} recall<strong>{{rate $value}}</strong></div>{{end}}<div class="metric">Observed tokens<strong>{{.Metrics.Tokens}}</strong></div><div class="metric">End-to-end case latency (includes fixture setup)<strong>{{printf "%.0f" .Metrics.MeanDurationMillis}} ms mean / {{.Metrics.P95DurationMillis}} ms P95</strong></div></div>
<p>Incomplete runs remain in recall and gate denominators. Repeated runs are correlated, so no independent-sample confidence interval is asserted. Zero-support rates are N/A. Finding matching requires priority, category, path, line range and a declared evidence phrase; unresolved hypotheses do not count as true positives.</p>
<h2>Case results</h2><div class="scroll"><table><thead><tr><th>Case / repeat</th><th>Expected → actual gate</th><th>Matched / missed / unexpected</th><th>Status</th></tr></thead><tbody>{{range .Runs}}<tr><td>{{.CaseID}} / {{.Repeat}}<br>{{.Title}}</td><td>{{.ExpectedGate}} → {{.ActualGate}}</td><td>{{len .MatchedIDs}} / {{len .MissedIDs}} / {{len .Unexpected}}</td><td>{{if .Passed}}<span class="pass">PASS</span>{{else}}<span class="fail">{{if .Incomplete}}INCOMPLETE{{else}}FAIL{{end}}</span>{{end}}</td></tr>{{end}}</tbody></table></div>
{{range .Runs}}<details><summary>{{.CaseID}} · repeat {{.Repeat}} · fresh evidence and independent labels</summary>{{if .Error}}<p class="fail">{{.Error}}</p>{{end}}<p>Base <code>{{.BaseCommit}}</code><br>Head <code>{{.HeadCommit}}</code></p><h3>Independent labels</h3><pre>{{json .Expected}}</pre><p>Matched: {{range .MatchedIDs}}{{.}} {{end}}<br>Missed: {{range .MissedIDs}}{{.}} {{end}}</p><h3>Fresh pipeline report</h3><pre>{{json .Review}}</pre></details>{{end}}
<h2>Reproduction and limits</h2><p>Provider: {{.Provider}} · requested model: {{.RequestedModel}} · analyzers: {{.Analyzers}}<br>Verifier enabled: {{.VerifierEnabled}} · Go toolchain: {{.GoToolchain}}<br>Gate: findings {{.Gate.FailOn}}, unresolved {{.Gate.FailOnNeedsReview}}, incomplete {{.Gate.FailOnIncomplete}}<br>Corpus SHA-256: <code>{{.CorpusSHA256}}</code><br>Trusted binary SHA-256: <code>{{.BinarySHA256}}</code></p><ul>{{range .Limitations}}<li>{{.}}</li>{{end}}</ul><footer>{{.SchemaVersion}} · Synthetic local fixtures · Fresh outputs, not golden report replay</footer></main></body></html>`))
