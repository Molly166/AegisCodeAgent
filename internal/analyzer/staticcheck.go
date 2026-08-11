package analyzer

import (
	"context"
	"encoding/json"
	"regexp"
	"strings"

	"github.com/Molly166/AegisCodeAgent/internal/review"
)

var staticcheckRulePattern = regexp.MustCompile(`\s+\(([A-Z]+\d+)\)\s*$`)

type StaticcheckAnalyzer struct {
	runner Runner
}

func NewStaticcheckAnalyzer(runner Runner) StaticcheckAnalyzer {
	return StaticcheckAnalyzer{runner: runner}
}

func (a StaticcheckAnalyzer) Name() string { return NameStaticcheck }

func (a StaticcheckAnalyzer) Analyze(ctx context.Context, input Input) ([]review.Finding, error) {
	if err := requirePackages(input); err != nil {
		return nil, err
	}
	if err := ensureTool(a.runner, NameStaticcheck); err != nil {
		return nil, err
	}
	arguments := []string{"-f", "json"}
	arguments = append(arguments, input.Packages...)
	execution, err := a.runner.Run(ctx, Command{Name: NameStaticcheck, Arguments: arguments, Directory: input.Repository})
	if err != nil {
		return nil, err
	}
	findings := parseStaticcheckDiagnostics(input.Repository, execution)
	if execution.ExitCode != 0 && len(findings) == 0 {
		return nil, commandFailure(NameStaticcheck, execution)
	}
	return findings, nil
}

type staticcheckDiagnostic struct {
	Code     string `json:"code"`
	Severity string `json:"severity"`
	Location struct {
		File   string `json:"file"`
		Line   int    `json:"line"`
		Column int    `json:"column"`
	} `json:"location"`
	End struct {
		Line int `json:"line"`
	} `json:"end"`
	Message string `json:"message"`
}

func parseStaticcheckDiagnostics(repository string, execution Execution) []review.Finding {
	findings := make([]review.Finding, 0)
	consume := func(line string) {
		var item staticcheckDiagnostic
		if json.Unmarshal([]byte(line), &item) == nil && item.Code != "" {
			findings = append(findings, newStaticcheckFinding(
				item.Code,
				item.Message,
				review.Location{
					Path:      normalizePath(repository, item.Location.File),
					StartLine: item.Location.Line,
					EndLine:   item.End.Line,
				},
				line,
			))
			return
		}
		diagnostic, ok := parseDiagnosticLine(repository, line)
		if !ok {
			return
		}
		ruleID := NameStaticcheck
		message := diagnostic.Message
		if matches := staticcheckRulePattern.FindStringSubmatch(message); matches != nil {
			ruleID = matches[1]
			message = strings.TrimSpace(staticcheckRulePattern.ReplaceAllString(message, ""))
		}
		findings = append(findings, newStaticcheckFinding(
			ruleID,
			message,
			review.Location{Path: diagnostic.Path, StartLine: diagnostic.Line},
			diagnostic.Raw,
		))
	}
	scanLines(execution.Stdout, consume)
	scanLines(execution.Stderr, consume)
	return findings
}

func newStaticcheckFinding(ruleID, message string, location review.Location, evidence string) review.Finding {
	severity, category := classifyStaticcheckRule(ruleID)
	return review.Finding{
		RuleID:      ruleID,
		Title:       shortText(message, 110),
		Description: "Staticcheck reported a deterministic Go diagnostic.",
		Severity:    severity,
		Category:    category,
		Location:    location,
		Evidence:    evidence,
		Suggestion:  "Resolve the diagnostic and rerun staticcheck for the affected package.",
		Confidence:  0.95,
		Source:      NameStaticcheck,
	}
}

func classifyStaticcheckRule(ruleID string) (review.Severity, review.Category) {
	switch {
	case strings.HasPrefix(ruleID, "SA"):
		return review.SeverityHigh, review.CategoryBug
	case strings.HasPrefix(ruleID, "ST"), strings.HasPrefix(ruleID, "QF"):
		return review.SeverityInfo, review.CategoryMaintainability
	case strings.HasPrefix(ruleID, "S"):
		return review.SeverityLow, review.CategoryMaintainability
	case strings.HasPrefix(ruleID, "U"):
		return review.SeverityLow, review.CategoryMaintainability
	default:
		return review.SeverityMedium, review.CategoryBug
	}
}
