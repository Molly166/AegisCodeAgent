package report

import (
	"bytes"
	"encoding/json"
	"fmt"
	"html"
	"strconv"
	"strings"

	"github.com/Molly166/AegisCodeAgent/internal/review"
)

const (
	FormatHTML     = "html"
	FormatMarkdown = "markdown"
	FormatJSON     = "json"
)

func Render(format string, reviewReport review.ReviewReport) ([]byte, error) {
	switch strings.ToLower(format) {
	case FormatHTML:
		return RenderHTML(reviewReport)
	case FormatMarkdown, "md":
		return RenderMarkdown(reviewReport), nil
	case FormatJSON:
		return RenderJSON(reviewReport)
	default:
		return nil, fmt.Errorf("unsupported report format %q (supported: html, markdown, json)", format)
	}
}

func IsSupported(format string) bool {
	switch strings.ToLower(format) {
	case FormatHTML, FormatMarkdown, "md", FormatJSON:
		return true
	default:
		return false
	}
}

func RenderJSON(reviewReport review.ReviewReport) ([]byte, error) {
	output, err := json.MarshalIndent(reviewReport, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode JSON report: %w", err)
	}
	return append(output, '\n'), nil
}

func RenderMarkdown(reviewReport review.ReviewReport) []byte {
	var output bytes.Buffer
	comparison := reviewReport.Comparison

	output.WriteString("# AegisCodeAgent Review\n\n")
	fmt.Fprintf(&output, "- Repository: %s\n", code(comparison.Repository))
	fmt.Fprintf(&output, "- Comparison: %s → %s\n", code(comparison.Base), code(comparison.Head))
	fmt.Fprintf(&output, "- Commits: %s → %s\n", code(shortCommit(comparison.BaseCommit)), code(shortCommit(comparison.HeadCommit)))
	fmt.Fprintf(&output, "- Analysis: %s\n", code(string(reviewReport.Analysis.Status)))
	fmt.Fprintf(&output, "- Generated: %s\n\n", code(reviewReport.GeneratedAt.UTC().Format("2006-01-02T15:04:05Z")))

	output.WriteString("## Summary\n\n")
	output.WriteString("| Changed files | Additions | Deletions | Findings | Critical | High | Medium | Low |\n")
	output.WriteString("| ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |\n")
	fmt.Fprintf(
		&output,
		"| %d | +%d | -%d | %d | %d | %d | %d | %d |\n\n",
		reviewReport.Summary.ChangedFiles,
		reviewReport.Summary.Additions,
		reviewReport.Summary.Deletions,
		reviewReport.Summary.Findings,
		reviewReport.Summary.Critical,
		reviewReport.Summary.High,
		reviewReport.Summary.Medium,
		reviewReport.Summary.Low,
	)

	output.WriteString("## Changed files\n\n")
	if len(reviewReport.Files) == 0 {
		output.WriteString("_No changed files were found for this comparison._\n\n")
	} else {
		output.WriteString("| File | Status | Binary | Additions | Deletions |\n")
		output.WriteString("| --- | --- | :---: | ---: | ---: |\n")
		for _, file := range reviewReport.Files {
			fmt.Fprintf(
				&output,
				"| %s | %s | %s | +%d | -%d |\n",
				code(file.Path()),
				escapeTable(string(file.Status)),
				map[bool]string{true: "yes", false: "no"}[file.Binary],
				file.Stats.Additions,
				file.Stats.Deletions,
			)
		}
		output.WriteByte('\n')
	}
	if len(reviewReport.Analysis.Tools) > 0 {
		output.WriteString("## Analyzer execution\n\n")
		output.WriteString("| Analyzer | Status | Duration | Findings | Suppressed | Detail |\n")
		output.WriteString("| --- | --- | ---: | ---: | ---: | --- |\n")
		for _, tool := range reviewReport.Analysis.Tools {
			fmt.Fprintf(
				&output,
				"| %s | %s | %d ms | %d | %d | %s |\n",
				code(tool.Name),
				code(string(tool.Status)),
				tool.DurationMillis,
				tool.Findings,
				tool.Suppressed,
				escapeTable(tool.Detail),
			)
		}
		output.WriteByte('\n')
	}
	if reviewReport.Context.Status != review.ContextNotRun {
		output.WriteString("## Repository context\n\n")
		fmt.Fprintf(&output, "- Status: %s\n", code(string(reviewReport.Context.Status)))
		fmt.Fprintf(&output, "- Packages/files indexed: %d / %d\n", reviewReport.Context.Stats.PackagesLoaded, reviewReport.Context.Stats.FilesParsed)
		fmt.Fprintf(&output, "- Packages type-checked/failures: %d / %d\n", reviewReport.Context.Stats.PackagesTypeChecked, reviewReport.Context.Stats.TypeCheckFailures)
		fmt.Fprintf(&output, "- Symbols indexed/selected: %d / %d\n", reviewReport.Context.Stats.SymbolsIndexed, reviewReport.Context.Stats.SymbolsSelected)
		fmt.Fprintf(&output, "- Relations: %d\n", reviewReport.Context.Stats.RelationsIndexed)
		fmt.Fprintf(&output, "- Estimated tokens: %d\n", reviewReport.Context.Stats.EstimatedTokens)
		fmt.Fprintf(&output, "- Truncated: %t\n\n", reviewReport.Context.Truncated)
		if len(reviewReport.Context.ChangedSymbols) > 0 {
			output.WriteString("### Changed symbols\n\n")
			for _, symbol := range reviewReport.Context.ChangedSymbols {
				fmt.Fprintf(&output, "- %s — %s (%s)\n", code(symbol.QualifiedName), code(formatSymbolLocation(symbol)), code(string(symbol.Kind)))
			}
			output.WriteByte('\n')
		}
		if len(reviewReport.Context.RelatedSymbols) > 0 {
			output.WriteString("### Related symbols\n\n")
			for _, symbol := range reviewReport.Context.RelatedSymbols {
				fmt.Fprintf(
					&output,
					"- %s — score %d; %s\n",
					code(symbol.QualifiedName),
					symbol.RelevanceScore,
					escapeTable(strings.Join(symbol.Reasons, ", ")),
				)
			}
			output.WriteByte('\n')
		}
		if len(reviewReport.Context.Warnings) > 0 {
			output.WriteString("### Context warnings\n\n")
			for _, warning := range reviewReport.Context.Warnings {
				fmt.Fprintf(&output, "- %s\n", warning)
			}
			output.WriteByte('\n')
		}
	}
	if reviewReport.Agent.Status != review.AgentNotRun {
		output.WriteString("## Reasoning agent\n\n")
		fmt.Fprintf(&output, "- Status: %s\n", code(string(reviewReport.Agent.Status)))
		fmt.Fprintf(&output, "- Provider / model: %s / %s\n", code(reviewReport.Agent.Provider), code(reviewReport.Agent.Model))
		fmt.Fprintf(&output, "- Thinking: %t\n", reviewReport.Agent.Thinking)
		fmt.Fprintf(&output, "- Steps / duration: %d / %d ms\n", reviewReport.Agent.Steps, reviewReport.Agent.DurationMillis)
		fmt.Fprintf(&output, "- Tokens: %d prompt / %d completion / %d total\n", reviewReport.Agent.Usage.PromptTokens, reviewReport.Agent.Usage.CompletionTokens, reviewReport.Agent.Usage.TotalTokens)
		fmt.Fprintf(&output, "- Unverified candidates: %d\n\n", len(reviewReport.Agent.Candidates))
		output.WriteString("> Agent candidates are hypotheses. They do not affect the review verdict until the verifier accepts them.\n\n")
		if reviewReport.Agent.Summary != "" {
			fmt.Fprintf(&output, "%s\n\n", reviewReport.Agent.Summary)
		}
		if len(reviewReport.Agent.ToolCalls) > 0 {
			output.WriteString("### Agent tool audit\n\n")
			output.WriteString("| Step | Tool | Status | Duration | Result |\n")
			output.WriteString("| ---: | --- | --- | ---: | --- |\n")
			for _, tool := range reviewReport.Agent.ToolCalls {
				fmt.Fprintf(&output, "| %d | %s | %s | %d ms | %s |\n", tool.Step, code(tool.Name), code(string(tool.Status)), tool.DurationMillis, escapeTable(tool.ResultSummary))
			}
			output.WriteByte('\n')
		}
		if len(reviewReport.Agent.Candidates) > 0 {
			output.WriteString("### Unverified candidates\n\n")
			for index, candidate := range reviewReport.Agent.Candidates {
				fmt.Fprintf(&output, "#### %d. %s\n\n", index+1, html.EscapeString(candidate.Title))
				fmt.Fprintf(&output, "- Status: %s\n", code("agent-candidate"))
				fmt.Fprintf(&output, "- Severity / category: %s / %s\n", code(string(candidate.Severity)), code(string(candidate.Category)))
				fmt.Fprintf(&output, "- Location: %s\n", code(formatLocation(candidate.Location)))
				fmt.Fprintf(&output, "- Confidence: %.0f%%\n\n", candidate.Confidence*100)
				fmt.Fprintf(&output, "%s\n\n", candidate.Description)
				output.WriteString("**Evidence**\n\n")
				writeBlockquote(&output, candidate.Evidence)
				fmt.Fprintf(&output, "\n**Suggested correction**\n\n%s\n\n", candidate.Suggestion)
				output.WriteString("**Verification plan**\n\n")
				for _, step := range candidate.Verification {
					fmt.Fprintf(&output, "- %s\n", step)
				}
				output.WriteByte('\n')
			}
		}
		if len(reviewReport.Agent.Warnings) > 0 {
			output.WriteString("### Agent warnings\n\n")
			for _, warning := range reviewReport.Agent.Warnings {
				fmt.Fprintf(&output, "- %s\n", warning)
			}
			output.WriteByte('\n')
		}
	}
	if reviewReport.Verification.Status != review.VerificationNotRun {
		output.WriteString("## Verification\n\n")
		fmt.Fprintf(&output, "- Status: %s\n", code(string(reviewReport.Verification.Status)))
		fmt.Fprintf(&output, "- Duration: %d ms\n", reviewReport.Verification.DurationMillis)
		fmt.Fprintf(&output, "- Candidates: %d\n", reviewReport.Verification.Summary.Candidates)
		fmt.Fprintf(&output, "- Verified / rejected / inconclusive: %d / %d / %d\n", reviewReport.Verification.Summary.Verified, reviewReport.Verification.Summary.Rejected, reviewReport.Verification.Summary.Inconclusive)
		fmt.Fprintf(&output, "- Promoted findings: %d\n\n", reviewReport.Verification.Summary.Promoted)
		if len(reviewReport.Verification.Tools) > 0 {
			output.WriteString("### Focused verification tools\n\n")
			output.WriteString("| Tool | Status | Duration | Findings | Detail |\n")
			output.WriteString("| --- | --- | ---: | ---: | --- |\n")
			for _, tool := range reviewReport.Verification.Tools {
				fmt.Fprintf(&output, "| %s | %s | %d ms | %d | %s |\n", code(tool.Name), code(string(tool.Status)), tool.DurationMillis, tool.Findings, escapeTable(tool.Detail))
			}
			output.WriteByte('\n')
		}
		for index, candidate := range reviewReport.Verification.Candidates {
			fmt.Fprintf(&output, "### %d. %s\n\n", index+1, html.EscapeString(candidate.Title))
			fmt.Fprintf(&output, "- Verdict: %s\n", code(string(candidate.Verdict)))
			fmt.Fprintf(&output, "- Location: %s\n", code(formatLocation(candidate.Location)))
			fmt.Fprintf(&output, "- Calibrated confidence: %.0f%%\n", candidate.CalibratedConfidence*100)
			if candidate.FindingID != "" {
				fmt.Fprintf(&output, "- Finding: %s%s\n", code(candidate.FindingID), map[bool]string{true: " (promoted)", false: " (existing)"}[candidate.Promoted])
			}
			fmt.Fprintf(&output, "\n%s\n\n", candidate.Reason)
			if candidate.SourceSnapshot != "" {
				output.WriteString("**Source snapshot**\n\n")
				writeBlockquote(&output, candidate.SourceSnapshot)
				output.WriteByte('\n')
			}
			output.WriteString("**Verification checks**\n\n")
			for _, check := range candidate.Checks {
				fmt.Fprintf(&output, "- %s — %s: %s\n", code(string(check.Status)), check.Name, check.Detail)
			}
			output.WriteByte('\n')
		}
		if len(reviewReport.Verification.Warnings) > 0 {
			output.WriteString("### Verification warnings\n\n")
			for _, warning := range reviewReport.Verification.Warnings {
				fmt.Fprintf(&output, "- %s\n", warning)
			}
			output.WriteByte('\n')
		}
	}

	output.WriteString("## Findings\n\n")
	if len(reviewReport.Findings) == 0 {
		switch reviewReport.Analysis.Status {
		case review.AnalysisScopeOnly:
			output.WriteString("_Review analysis has not run; this report maps change scope only._\n")
		case review.AnalysisPartial:
			output.WriteString("_No findings from completed analyzers; at least one requested analyzer did not complete._\n")
		case review.AnalysisFailed:
			output.WriteString("_Review analysis failed. Inspect analyzer execution details and run the review again._\n")
		default:
			output.WriteString("_No findings were produced._\n")
		}
		return output.Bytes()
	}

	for index, finding := range reviewReport.Findings {
		fmt.Fprintf(&output, "### %d. %s\n\n", index+1, html.EscapeString(finding.Title))
		fmt.Fprintf(&output, "- Severity: %s\n", code(string(finding.Severity)))
		fmt.Fprintf(&output, "- Category: %s\n", code(string(finding.Category)))
		fmt.Fprintf(&output, "- Location: %s\n", code(formatLocation(finding.Location)))
		fmt.Fprintf(&output, "- Confidence: %.0f%%\n", finding.Confidence*100)
		if finding.Source != "" {
			fmt.Fprintf(&output, "- Source: %s\n", code(finding.Source))
		}
		output.WriteByte('\n')
		if finding.Description != "" {
			fmt.Fprintf(&output, "%s\n\n", finding.Description)
		}
		if finding.Evidence != "" {
			output.WriteString("**Evidence**\n\n")
			writeBlockquote(&output, finding.Evidence)
			output.WriteByte('\n')
		}
		if finding.Suggestion != "" {
			output.WriteString("**Suggestion**\n\n")
			fmt.Fprintf(&output, "%s\n\n", finding.Suggestion)
		}
	}
	return output.Bytes()
}

func formatLocation(location review.Location) string {
	if location.StartLine <= 0 {
		return location.Path
	}
	if location.EndLine > location.StartLine {
		return location.Path + ":" + strconv.Itoa(location.StartLine) + "-" + strconv.Itoa(location.EndLine)
	}
	return location.Path + ":" + strconv.Itoa(location.StartLine)
}

func formatSymbolLocation(symbol review.ContextSymbol) string {
	if symbol.StartLine <= 0 {
		return symbol.Path
	}
	if symbol.EndLine > symbol.StartLine {
		return symbol.Path + ":" + strconv.Itoa(symbol.StartLine) + "-" + strconv.Itoa(symbol.EndLine)
	}
	return symbol.Path + ":" + strconv.Itoa(symbol.StartLine)
}

func writeBlockquote(output *bytes.Buffer, value string) {
	for _, line := range strings.Split(value, "\n") {
		fmt.Fprintf(output, "> %s\n", line)
	}
}

func code(value string) string {
	return "<code>" + html.EscapeString(value) + "</code>"
}

func escapeTable(value string) string {
	return strings.ReplaceAll(html.EscapeString(value), "|", "\\|")
}

func shortCommit(commit string) string {
	if len(commit) <= 12 {
		return commit
	}
	return commit[:12]
}
