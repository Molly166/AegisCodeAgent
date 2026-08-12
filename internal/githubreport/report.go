package githubreport

import (
	"bytes"
	"fmt"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/Molly166/AegisCodeAgent/internal/review"
)

const DefaultMaxAnnotations = 50

type Priority string

const (
	PriorityP0   Priority = "p0"
	PriorityP1   Priority = "p1"
	PriorityP2   Priority = "p2"
	PriorityP3   Priority = "p3"
	PriorityNone Priority = "none"
)

type Counts struct {
	P0 int
	P1 int
	P2 int
	P3 int
}

type GateResult struct {
	Blocked           bool
	Incomplete        bool
	Highest           Priority
	IncompleteReasons []string
}

type Options struct {
	FailOn           Priority
	FailOnIncomplete bool
	ArtifactName     string
	MaxFindings      int
}

func ParsePriority(value string) (Priority, error) {
	priority := Priority(strings.ToLower(strings.TrimSpace(value)))
	switch priority {
	case PriorityP0, PriorityP1, PriorityP2, PriorityP3, PriorityNone:
		return priority, nil
	default:
		return "", fmt.Errorf("unsupported priority %q (supported: p0, p1, p2, p3, none)", value)
	}
}

func PriorityForSeverity(severity review.Severity) Priority {
	switch severity {
	case review.SeverityCritical:
		return PriorityP0
	case review.SeverityHigh:
		return PriorityP1
	case review.SeverityMedium:
		return PriorityP2
	default:
		return PriorityP3
	}
}

func Count(report review.ReviewReport) Counts {
	var counts Counts
	for _, finding := range report.Findings {
		switch PriorityForSeverity(finding.Severity) {
		case PriorityP0:
			counts.P0++
		case PriorityP1:
			counts.P1++
		case PriorityP2:
			counts.P2++
		case PriorityP3:
			counts.P3++
		}
	}
	return counts
}

func Evaluate(report review.ReviewReport, failOn Priority, failOnIncomplete bool) GateResult {
	result := GateResult{
		Highest:           highestPriority(report.Findings),
		IncompleteReasons: incompleteReasons(report),
	}
	result.Incomplete = len(result.IncompleteReasons) > 0
	result.Blocked = priorityBlocks(result.Highest, failOn) || (failOnIncomplete && result.Incomplete)
	return result
}

func RenderSummary(report review.ReviewReport, options Options) []byte {
	if options.FailOn == "" {
		options.FailOn = PriorityP1
	}
	if options.ArtifactName == "" {
		options.ArtifactName = "aegis-review-report"
	}
	if options.MaxFindings <= 0 {
		options.MaxFindings = 20
	}

	gate := Evaluate(report, options.FailOn, options.FailOnIncomplete)
	counts := Count(report)
	var output bytes.Buffer
	output.WriteString("# 🛡️ Aegis Code Review\n\n")
	switch {
	case gate.Incomplete && options.FailOnIncomplete:
		output.WriteString("> ❌ **Review incomplete — merge gate blocked.** Inspect the stage status and rerun Aegis.\n\n")
	case gate.Blocked:
		fmt.Fprintf(&output, "> ❌ **Merge gate blocked.** A finding met the `%s` failure threshold.\n\n", strings.ToUpper(string(options.FailOn)))
	case len(report.Findings) > 0:
		output.WriteString("> ⚠️ **Review completed with non-blocking findings.**\n\n")
	default:
		output.WriteString("> ✅ **Review completed without findings.**\n\n")
	}

	output.WriteString("| P0 | P1 | P2 | P3 | Files | Findings |\n")
	output.WriteString("| ---: | ---: | ---: | ---: | ---: | ---: |\n")
	fmt.Fprintf(&output, "| %d | %d | %d | %d | %d | %d |\n\n", counts.P0, counts.P1, counts.P2, counts.P3, report.Summary.ChangedFiles, len(report.Findings))

	fmt.Fprintf(&output, "- **Comparison:** `%s` → `%s`\n", markdownCode(report.Comparison.Base), markdownCode(report.Comparison.Head))
	fmt.Fprintf(&output, "- **Analysis:** `%s`\n", report.Analysis.Status)
	fmt.Fprintf(&output, "- **Repository context:** `%s`\n", report.Context.Status)
	fmt.Fprintf(&output, "- **Reasoning Agent:** `%s`", report.Agent.Status)
	if report.Agent.Provider != "" || report.Agent.Model != "" {
		fmt.Fprintf(&output, " (`%s` / `%s`)", markdownCode(report.Agent.Provider), markdownCode(report.Agent.Model))
	}
	output.WriteByte('\n')
	fmt.Fprintf(&output, "- **Verifier:** `%s`", report.Verification.Status)
	if report.Verification.Status != review.VerificationNotRun {
		fmt.Fprintf(&output, " — %d verified, %d rejected, %d inconclusive", report.Verification.Summary.Verified, report.Verification.Summary.Rejected, report.Verification.Summary.Inconclusive)
	}
	output.WriteString("\n")
	fmt.Fprintf(&output, "- **Merge threshold:** `%s`\n", strings.ToUpper(string(options.FailOn)))
	fmt.Fprintf(&output, "- **Full evidence report:** download the `%s` workflow artifact.\n\n", markdownCode(options.ArtifactName))

	if len(gate.IncompleteReasons) > 0 {
		output.WriteString("## Incomplete stages\n\n")
		for _, reason := range gate.IncompleteReasons {
			fmt.Fprintf(&output, "- %s\n", markdownText(reason))
		}
		output.WriteByte('\n')
	}

	if len(report.Findings) == 0 {
		output.WriteString("Only final evidence-bearing findings are eligible for the merge gate. Agent hypotheses that remain inconclusive are withheld.\n")
		return output.Bytes()
	}

	output.WriteString("## Findings\n\n")
	output.WriteString("| Priority | Location | Finding | Source |\n")
	output.WriteString("| :---: | --- | --- | --- |\n")
	limit := min(len(report.Findings), options.MaxFindings)
	for _, finding := range report.Findings[:limit] {
		fmt.Fprintf(
			&output,
			"| **%s** | `%s` | %s | `%s` |\n",
			strings.ToUpper(string(PriorityForSeverity(finding.Severity))),
			markdownCode(formatLocation(finding.Location)),
			markdownCell(finding.Title),
			markdownCode(finding.Source),
		)
	}
	if omitted := len(report.Findings) - limit; omitted > 0 {
		fmt.Fprintf(&output, "\n_%d additional finding(s) are available in the HTML artifact._\n", omitted)
	}
	output.WriteString("\n> P0/P1 annotations are merge-blocking by default. P2/P3 findings remain visible without blocking the pull request.\n")
	return output.Bytes()
}

func RenderAnnotations(report review.ReviewReport, maximum int) []byte {
	if maximum <= 0 {
		maximum = DefaultMaxAnnotations
	}
	findings := append([]review.Finding(nil), report.Findings...)
	sort.SliceStable(findings, func(i, j int) bool {
		left, right := priorityRank(PriorityForSeverity(findings[i].Severity)), priorityRank(PriorityForSeverity(findings[j].Severity))
		if left != right {
			return left < right
		}
		if findings[i].Location.Path != findings[j].Location.Path {
			return findings[i].Location.Path < findings[j].Location.Path
		}
		return findings[i].Location.StartLine < findings[j].Location.StartLine
	})

	var output bytes.Buffer
	for _, finding := range findings[:min(len(findings), maximum)] {
		priority := PriorityForSeverity(finding.Severity)
		level := annotationLevel(priority)
		title := truncateUTF8(strings.ToUpper(string(priority))+" · "+plainText(finding.Title), 240)
		properties := []string{"title=" + escapeProperty(title)}
		if path, ok := annotationPath(finding.Location.Path); ok {
			properties = append(properties, "file="+escapeProperty(path))
			if finding.Location.StartLine > 0 {
				properties = append(properties, "line="+strconv.Itoa(finding.Location.StartLine))
				if finding.Location.EndLine >= finding.Location.StartLine {
					properties = append(properties, "endLine="+strconv.Itoa(finding.Location.EndLine))
				}
			}
		}
		message := annotationMessage(finding)
		fmt.Fprintf(&output, "::%s %s::%s\n", level, strings.Join(properties, ","), escapeData(message))
	}
	if omitted := len(findings) - min(len(findings), maximum); omitted > 0 {
		fmt.Fprintf(&output, "::notice title=Aegis annotation limit::%d additional finding(s) are available in the HTML artifact.\n", omitted)
	}
	return output.Bytes()
}

func incompleteReasons(report review.ReviewReport) []string {
	reasons := make([]string, 0, 4)
	switch report.Analysis.Status {
	case review.AnalysisScopeOnly:
		reasons = append(reasons, "Deterministic analysis was not run.")
	case review.AnalysisPartial:
		reasons = append(reasons, "Deterministic analysis completed only partially.")
	case review.AnalysisFailed:
		reasons = append(reasons, "Deterministic analysis failed.")
	}
	switch report.Context.Status {
	case review.ContextPartial:
		reasons = append(reasons, "Repository context indexing completed only partially.")
	case review.ContextFailed:
		reasons = append(reasons, "Repository context indexing failed.")
	}
	switch report.Agent.Status {
	case review.AgentPartial:
		reasons = append(reasons, "Reasoning Agent completed only partially.")
	case review.AgentFailed:
		reasons = append(reasons, "Reasoning Agent failed.")
	}
	switch report.Verification.Status {
	case review.VerificationPartial:
		reasons = append(reasons, "Candidate verification completed only partially.")
	case review.VerificationFailed:
		reasons = append(reasons, "Candidate verification failed.")
	}
	return reasons
}

func highestPriority(findings []review.Finding) Priority {
	highest := PriorityNone
	for _, finding := range findings {
		priority := PriorityForSeverity(finding.Severity)
		if priorityRank(priority) < priorityRank(highest) {
			highest = priority
		}
	}
	return highest
}

func priorityBlocks(actual, threshold Priority) bool {
	return threshold != PriorityNone && actual != PriorityNone && priorityRank(actual) <= priorityRank(threshold)
}

func priorityRank(priority Priority) int {
	switch priority {
	case PriorityP0:
		return 0
	case PriorityP1:
		return 1
	case PriorityP2:
		return 2
	case PriorityP3:
		return 3
	default:
		return 4
	}
}

func annotationLevel(priority Priority) string {
	switch priority {
	case PriorityP0, PriorityP1:
		return "error"
	case PriorityP2:
		return "warning"
	default:
		return "notice"
	}
}

func annotationMessage(finding review.Finding) string {
	parts := make([]string, 0, 4)
	if value := plainText(finding.Description); value != "" {
		parts = append(parts, value)
	}
	if value := plainText(finding.Evidence); value != "" {
		parts = append(parts, "Evidence: "+value)
	}
	if value := plainText(finding.Suggestion); value != "" {
		parts = append(parts, "Suggestion: "+value)
	}
	if finding.Source != "" {
		parts = append(parts, "Source: "+plainText(finding.Source))
	}
	if len(parts) == 0 {
		parts = append(parts, plainText(finding.Title))
	}
	return truncateUTF8(strings.Join(parts, " | "), 4000)
}

func annotationPath(path string) (string, bool) {
	path = filepath.ToSlash(filepath.Clean(strings.TrimSpace(path)))
	if path == "" || path == "." || filepath.IsAbs(path) || path == ".." || strings.HasPrefix(path, "../") {
		return "", false
	}
	return path, true
}

func formatLocation(location review.Location) string {
	path := location.Path
	if location.StartLine <= 0 {
		return path
	}
	if location.EndLine > location.StartLine {
		return fmt.Sprintf("%s:%d-%d", path, location.StartLine, location.EndLine)
	}
	return fmt.Sprintf("%s:%d", path, location.StartLine)
}

func markdownCell(value string) string {
	return strings.ReplaceAll(markdownText(value), "|", "\\|")
}

func markdownText(value string) string {
	return strings.Join(strings.Fields(plainText(value)), " ")
}

func markdownCode(value string) string {
	return strings.ReplaceAll(plainText(value), "`", "ˋ")
}

func plainText(value string) string {
	return strings.Map(func(r rune) rune {
		if r == '\n' || r == '\r' || r == '\t' {
			return ' '
		}
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, strings.TrimSpace(value))
}

func escapeData(value string) string {
	value = strings.ReplaceAll(value, "%", "%25")
	value = strings.ReplaceAll(value, "\r", "%0D")
	return strings.ReplaceAll(value, "\n", "%0A")
}

func escapeProperty(value string) string {
	value = escapeData(value)
	value = strings.ReplaceAll(value, ":", "%3A")
	return strings.ReplaceAll(value, ",", "%2C")
}

func truncateUTF8(value string, maximum int) string {
	if maximum <= 0 || len(value) <= maximum {
		return value
	}
	value = value[:maximum]
	for !utf8.ValidString(value) {
		value = value[:len(value)-1]
	}
	return value + "…"
}
