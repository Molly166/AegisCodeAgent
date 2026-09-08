package analyzer

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"

	"github.com/Molly166/AegisCodeAgent/internal/review"
)

type GoTestAnalyzer struct {
	runner Runner
}

func NewGoTestAnalyzer(runner Runner) GoTestAnalyzer {
	return GoTestAnalyzer{runner: runner}
}

func (a GoTestAnalyzer) Name() string { return NameGoTest }

func (a GoTestAnalyzer) Analyze(ctx context.Context, input Input) ([]review.Finding, error) {
	if err := requirePackages(input); err != nil {
		return nil, err
	}
	if err := ensureTool(a.runner, "go"); err != nil {
		return nil, err
	}
	arguments := []string{"test", "-json", "-count=1"}
	arguments = append(arguments, input.Packages...)
	execution, err := a.runner.Run(ctx, Command{Name: "go", Arguments: arguments, Directory: input.Repository})
	findings := parseGoTestDiagnostics(input.Repository, execution)
	if err = executionError(execution, err); err != nil {
		return findings, err
	}
	if execution.ExitCode != 0 && len(findings) == 0 {
		findings = append(findings, review.Finding{
			RuleID:      NameGoTest,
			Title:       "Go tests or package build failed",
			Description: "The selected Go packages did not build or pass their test suite.",
			Severity:    review.SeverityHigh,
			Category:    review.CategoryTesting,
			Evidence:    tailText(execution.CombinedOutput(), 2000),
			Suggestion:  "Run the reported go test command locally, then fix the failing build or test before merging.",
			Confidence:  1,
			Source:      NameGoTest,
		})
	}
	return findings, nil
}

type goTestEvent struct {
	Action  string `json:"Action"`
	Package string `json:"Package"`
	Test    string `json:"Test"`
	Output  string `json:"Output"`
}

func parseGoTestDiagnostics(repository string, execution Execution) []review.Finding {
	findings := make([]review.Finding, 0)
	seen := make(map[string]struct{})
	consume := func(line string) {
		var event goTestEvent
		if json.Unmarshal([]byte(line), &event) == nil && event.Output != "" {
			line = strings.TrimSuffix(event.Output, "\n")
		}
		diagnostic, ok := parseDiagnosticLine(repository, line)
		if !ok {
			return
		}
		key := diagnostic.Path + ":" + strconv.Itoa(diagnostic.Line) + ":" + diagnostic.Message
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = struct{}{}
		findings = append(findings, review.Finding{
			RuleID:      NameGoTest,
			Title:       shortText(diagnostic.Message, 100),
			Description: "The Go test/build pipeline reported this failure.",
			Severity:    review.SeverityHigh,
			Category:    review.CategoryTesting,
			Location:    review.Location{Path: diagnostic.Path, StartLine: diagnostic.Line},
			Evidence:    diagnostic.Raw,
			Suggestion:  "Reproduce with go test and fix the failing build or assertion before merging.",
			Confidence:  1,
			Source:      NameGoTest,
		})
	}
	scanLines(execution.Stdout, consume)
	scanLines(execution.Stderr, consume)
	return findings
}
