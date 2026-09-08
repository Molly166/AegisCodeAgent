package analyzer

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"

	"github.com/Molly166/AegisCodeAgent/internal/review"
)

type GosecAnalyzer struct {
	runner Runner
}

func NewGosecAnalyzer(runner Runner) GosecAnalyzer {
	return GosecAnalyzer{runner: runner}
}

func (a GosecAnalyzer) Name() string { return NameGosec }

func (a GosecAnalyzer) Analyze(ctx context.Context, input Input) ([]review.Finding, error) {
	if err := requirePackages(input); err != nil {
		return nil, err
	}
	if err := ensureTool(a.runner, NameGosec); err != nil {
		return nil, err
	}
	arguments := []string{"-fmt=json", "-quiet"}
	arguments = append(arguments, input.Packages...)
	execution, err := a.runner.Run(ctx, Command{Name: NameGosec, Arguments: arguments, Directory: input.Repository})
	findings := parseGosecDiagnostics(input.Repository, execution)
	if err = executionError(execution, err); err != nil {
		return findings, err
	}
	if execution.ExitCode != 0 && len(findings) == 0 {
		return nil, commandFailure(NameGosec, execution)
	}
	return findings, nil
}

type gosecReport struct {
	Issues []struct {
		Severity   string          `json:"severity"`
		Confidence string          `json:"confidence"`
		RuleID     string          `json:"rule_id"`
		Details    string          `json:"details"`
		File       string          `json:"file"`
		Code       string          `json:"code"`
		Line       json.RawMessage `json:"line"`
	} `json:"Issues"`
}

func parseGosecDiagnostics(repository string, execution Execution) []review.Finding {
	for _, candidate := range []string{execution.Stdout, execution.Stderr} {
		var report gosecReport
		if json.Unmarshal([]byte(strings.TrimSpace(candidate)), &report) != nil {
			continue
		}
		findings := make([]review.Finding, 0, len(report.Issues))
		for _, issue := range report.Issues {
			startLine, endLine := parseLineRange(issue.Line)
			findings = append(findings, review.Finding{
				RuleID:      issue.RuleID,
				Title:       shortText(issue.Details, 110),
				Description: "Gosec reported a security-sensitive code pattern.",
				Severity:    mapGosecSeverity(issue.Severity),
				Category:    review.CategorySecurity,
				Location: review.Location{
					Path:      normalizePath(repository, issue.File),
					StartLine: startLine,
					EndLine:   endLine,
				},
				Evidence:   strings.TrimSpace(issue.Code),
				Suggestion: "Review the data flow and remediate the reported security rule before merging.",
				Confidence: mapGosecConfidence(issue.Confidence),
				Source:     NameGosec,
			})
		}
		return findings
	}
	return nil
}

func parseLineRange(raw json.RawMessage) (int, int) {
	value := strings.Trim(strings.TrimSpace(string(raw)), `"`)
	parts := strings.SplitN(value, "-", 2)
	start, _ := strconv.Atoi(parts[0])
	end := 0
	if len(parts) == 2 {
		end, _ = strconv.Atoi(parts[1])
	}
	return start, end
}

func mapGosecSeverity(value string) review.Severity {
	switch strings.ToUpper(value) {
	case "CRITICAL":
		return review.SeverityCritical
	case "HIGH":
		return review.SeverityHigh
	case "MEDIUM":
		return review.SeverityMedium
	case "LOW":
		return review.SeverityLow
	default:
		return review.SeverityMedium
	}
}

func mapGosecConfidence(value string) float64 {
	switch strings.ToUpper(value) {
	case "HIGH":
		return 0.99
	case "MEDIUM":
		return 0.85
	case "LOW":
		return 0.65
	default:
		return 0.75
	}
}
