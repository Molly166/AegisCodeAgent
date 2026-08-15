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
	Blocked              bool
	BlockedByFinding     bool
	BlockedByNeedsReview bool
	Incomplete           bool
	Degraded             bool
	NeedsReview          bool
	Highest              Priority
	NeedsReviewHighest   Priority
	IncompleteReasons    []string
	DegradedReasons      []string
}

type Options struct {
	FailOn            Priority `json:"fail_on"`
	FailOnNeedsReview Priority `json:"fail_on_needs_review"`
	FailOnIncomplete  bool     `json:"fail_on_incomplete"`
	ArtifactName      string   `json:"artifact_name,omitempty"`
	MaxFindings       int      `json:"max_findings,omitempty"`
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
	return EvaluateWithOptions(report, Options{
		FailOn: failOn, FailOnNeedsReview: PriorityP0, FailOnIncomplete: failOnIncomplete,
	})
}

func EvaluateWithOptions(report review.ReviewReport, options Options) GateResult {
	if options.FailOn == "" {
		options.FailOn = PriorityP1
	}
	if options.FailOnNeedsReview == "" {
		options.FailOnNeedsReview = PriorityP0
	}
	result := GateResult{
		Highest:            highestPriority(report.Findings),
		NeedsReviewHighest: highestNeedsReviewPriority(report.Verification.Candidates),
		IncompleteReasons:  incompleteReasons(report),
		DegradedReasons:    degradedReasons(report),
	}
	result.Incomplete = len(result.IncompleteReasons) > 0
	result.Degraded = len(result.DegradedReasons) > 0
	result.NeedsReview = result.NeedsReviewHighest != PriorityNone
	result.BlockedByFinding = priorityBlocks(result.Highest, options.FailOn)
	result.BlockedByNeedsReview = priorityBlocks(result.NeedsReviewHighest, options.FailOnNeedsReview)
	result.Blocked = result.BlockedByFinding ||
		result.BlockedByNeedsReview ||
		(options.FailOnIncomplete && result.Incomplete)
	return result
}

func RenderSummary(report review.ReviewReport, options Options) []byte {
	if options.FailOn == "" {
		options.FailOn = PriorityP1
	}
	if options.FailOnNeedsReview == "" {
		options.FailOnNeedsReview = PriorityP0
	}
	if options.ArtifactName == "" {
		options.ArtifactName = "aegis-review-report"
	}
	if options.MaxFindings <= 0 {
		options.MaxFindings = 20
	}

	gate := EvaluateWithOptions(report, options)
	counts := Count(report)
	var output bytes.Buffer
	output.WriteString("# 🛡️ Aegis Code Review\n\n")
	switch {
	case gate.Incomplete && options.FailOnIncomplete:
		output.WriteString("> ❌ **Review incomplete — merge gate blocked.** Inspect the stage status and rerun Aegis.\n\n")
	case priorityBlocks(gate.Highest, options.FailOn):
		fmt.Fprintf(&output, "> ❌ **Merge gate blocked.** A finding met the `%s` failure threshold.\n\n", strings.ToUpper(string(options.FailOn)))
	case priorityBlocks(gate.NeedsReviewHighest, options.FailOnNeedsReview):
		fmt.Fprintf(&output, "> ❌ **Merge gate blocked pending human review.** An unresolved `%s` hypothesis met the `%s` needs-review threshold.\n\n", strings.ToUpper(string(gate.NeedsReviewHighest)), strings.ToUpper(string(options.FailOnNeedsReview)))
	case gate.NeedsReview:
		output.WriteString("> ⚠️ **Review completed with unresolved hypotheses requiring human review.**\n\n")
	case gate.Degraded && len(report.Findings) > 0:
		output.WriteString("> ⚠️ **Review completed with degraded optional stages and non-blocking findings.** Deterministic evidence remains available.\n\n")
	case gate.Degraded:
		output.WriteString("> ⚠️ **Review completed with degraded optional stages.** Deterministic merge-gate evidence remains available.\n\n")
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
		fmt.Fprintf(&output, " — %d verified, %d needs review, %d rejected", report.Verification.Summary.Verified, needsReviewCount(report.Verification), report.Verification.Summary.Rejected)
	}
	output.WriteString("\n")
	fmt.Fprintf(&output, "- **Merge threshold:** `%s`\n", strings.ToUpper(string(options.FailOn)))
	fmt.Fprintf(&output, "- **Needs-review threshold:** `%s`\n", strings.ToUpper(string(options.FailOnNeedsReview)))
	fmt.Fprintf(&output, "- **Full evidence report:** download the `%s` workflow artifact.\n\n", markdownCode(options.ArtifactName))

	if len(gate.IncompleteReasons) > 0 {
		output.WriteString("## Incomplete stages\n\n")
		for _, reason := range gate.IncompleteReasons {
			fmt.Fprintf(&output, "- %s\n", markdownText(reason))
		}
		output.WriteByte('\n')
	}
	if len(gate.DegradedReasons) > 0 {
		output.WriteString("## Degraded optional stages\n\n")
		for _, reason := range gate.DegradedReasons {
			fmt.Fprintf(&output, "- %s\n", markdownText(reason))
		}
		output.WriteString("\nThese stages reduce review coverage but do not override deterministic P0/P1 merge-gate evidence.\n\n")
	}
	needsReview := unresolvedCandidates(report.Verification.Candidates)
	if len(needsReview) > 0 {
		output.WriteString("## Needs human review\n\n")
		output.WriteString("| Priority | Location | Hypothesis | Reason |\n")
		output.WriteString("| :---: | --- | --- | --- |\n")
		limit := min(len(needsReview), options.MaxFindings)
		for _, candidate := range needsReview[:limit] {
			fmt.Fprintf(&output, "| **%s** | `%s` | %s | %s |\n",
				strings.ToUpper(string(PriorityForSeverity(candidate.Severity))),
				markdownCode(formatLocation(candidate.Location)), markdownCell(candidate.Title), markdownCell(candidate.Reason))
		}
		output.WriteString("\nThese hypotheses were neither verified nor rejected. They are not a clean-review signal and may block according to the needs-review threshold.\n\n")
	}

	if len(report.Findings) == 0 {
		if len(needsReview) == 0 {
			if gate.Degraded {
				output.WriteString("No evidence-bearing findings or unresolved review hypotheses were produced; optional review coverage was degraded as listed above.\n")
			} else {
				output.WriteString("No evidence-bearing findings or unresolved review hypotheses were produced.\n")
			}
		}
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
	used := min(len(findings), maximum)
	for _, finding := range findings[:used] {
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
	unresolved := unresolvedCandidates(report.Verification.Candidates)
	remaining := maximum - used
	for _, candidate := range unresolved[:min(len(unresolved), remaining)] {
		priority := PriorityForSeverity(candidate.Severity)
		properties := []string{"title=" + escapeProperty(strings.ToUpper(string(priority))+" · NEEDS REVIEW · "+plainText(candidate.Title))}
		if path, ok := annotationPath(candidate.Location.Path); ok {
			properties = append(properties, "file="+escapeProperty(path))
			if candidate.Location.StartLine > 0 {
				properties = append(properties, "line="+strconv.Itoa(candidate.Location.StartLine))
			}
		}
		fmt.Fprintf(&output, "::warning %s::%s\n", strings.Join(properties, ","), escapeData(truncateUTF8(candidate.Reason, 4000)))
	}
	total := len(findings) + len(unresolved)
	if omitted := total - min(total, maximum); omitted > 0 {
		fmt.Fprintf(&output, "::notice title=Aegis annotation limit::%d additional finding(s) or unresolved hypothesis item(s) are available in the HTML artifact.\n", omitted)
	}
	return output.Bytes()
}

func unresolvedCandidates(candidates []review.CandidateVerification) []review.CandidateVerification {
	result := make([]review.CandidateVerification, 0)
	for _, candidate := range candidates {
		if candidate.Verdict == review.CandidateNeedsReview || candidate.Verdict == review.CandidateInconclusive {
			result = append(result, candidate)
		}
	}
	sort.SliceStable(result, func(i, j int) bool {
		left := priorityRank(PriorityForSeverity(result[i].Severity))
		right := priorityRank(PriorityForSeverity(result[j].Severity))
		if left != right {
			return left < right
		}
		if result[i].Location.Path != result[j].Location.Path {
			return result[i].Location.Path < result[j].Location.Path
		}
		return result[i].Location.StartLine < result[j].Location.StartLine
	})
	return result
}

func needsReviewCount(verification review.VerificationRun) int {
	return verification.Summary.NeedsReview + verification.Summary.Inconclusive
}

func highestNeedsReviewPriority(candidates []review.CandidateVerification) Priority {
	highest := PriorityNone
	for _, candidate := range unresolvedCandidates(candidates) {
		priority := PriorityForSeverity(candidate.Severity)
		if priorityRank(priority) < priorityRank(highest) {
			highest = priority
		}
	}
	return highest
}

func incompleteReasons(report review.ReviewReport) []string {
	reasons := make([]string, 0, 2)
	switch report.Analysis.Status {
	case review.AnalysisScopeOnly:
		reasons = append(reasons, "Deterministic analysis was not run.")
	case review.AnalysisPartial:
		reasons = append(reasons, "Deterministic analysis completed only partially.")
	case review.AnalysisFailed:
		reasons = append(reasons, "Deterministic analysis failed.")
	}
	switch report.Verification.Status {
	case review.VerificationPartial:
		if !verificationInheritedAgentDegradation(report) {
			reasons = append(reasons, "Candidate verification completed only partially.")
		}
	case review.VerificationFailed:
		reasons = append(reasons, "Candidate verification failed.")
	}
	return reasons
}

func degradedReasons(report review.ReviewReport) []string {
	reasons := make([]string, 0, 3)
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
	if verificationInheritedAgentDegradation(report) {
		reasons = append(reasons, "Candidate verification inherited the Reasoning Agent coverage warning; its own evidence checks did not fail.")
	}
	return reasons
}

func verificationInheritedAgentDegradation(report review.ReviewReport) bool {
	if report.Verification.Status != review.VerificationPartial || report.Agent.Status != review.AgentPartial || len(report.Verification.Warnings) == 0 {
		return false
	}
	for _, warning := range report.Verification.Warnings {
		switch strings.TrimSpace(warning) {
		case "reasoning agent was partial", "reasoning agent was partial; only returned candidates were verified":
		default:
			return false
		}
	}
	return true
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
