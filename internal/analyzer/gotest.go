package analyzer

import (
	"context"
	"errors"

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
	findings, parseErr := parseGoTestDiagnostics(input.Repository, execution)
	if err = executionError(execution, errors.Join(err, parseErr)); err != nil {
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
