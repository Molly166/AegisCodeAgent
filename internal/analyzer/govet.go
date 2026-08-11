package analyzer

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/Molly166/AegisCodeAgent/internal/review"
)

type GoVetAnalyzer struct {
	runner Runner
}

func NewGoVetAnalyzer(runner Runner) GoVetAnalyzer {
	return GoVetAnalyzer{runner: runner}
}

func (a GoVetAnalyzer) Name() string { return NameGoVet }

func (a GoVetAnalyzer) Analyze(ctx context.Context, input Input) ([]review.Finding, error) {
	if err := requirePackages(input); err != nil {
		return nil, err
	}
	if err := ensureTool(a.runner, "go"); err != nil {
		return nil, err
	}
	arguments := []string{"vet", "-json"}
	arguments = append(arguments, input.Packages...)
	execution, err := a.runner.Run(ctx, Command{Name: "go", Arguments: arguments, Directory: input.Repository})
	if err != nil {
		return nil, err
	}
	findings := parseGoVetDiagnostics(input.Repository, execution)
	if execution.ExitCode != 0 && len(findings) == 0 {
		return nil, commandFailure(NameGoVet, execution)
	}
	return findings, nil
}

type goVetDiagnostic struct {
	Position string `json:"posn"`
	Message  string `json:"message"`
}

func parseGoVetDiagnostics(repository string, execution Execution) []review.Finding {
	findings := make([]review.Finding, 0)
	for _, candidate := range []string{execution.Stdout, execution.Stderr} {
		var packages map[string]map[string][]goVetDiagnostic
		if json.Unmarshal([]byte(strings.TrimSpace(candidate)), &packages) != nil {
			continue
		}
		for _, analyzers := range packages {
			for ruleID, diagnostics := range analyzers {
				for _, item := range diagnostics {
					location, _ := parsePosition(repository, item.Position)
					findings = append(findings, newGoVetFinding(ruleID, item.Message, item.Position, location))
				}
			}
		}
	}
	if len(findings) > 0 {
		return findings
	}
	consumeText := func(line string) {
		diagnostic, ok := parseDiagnosticLine(repository, line)
		if !ok {
			return
		}
		findings = append(findings, newGoVetFinding(
			NameGoVet,
			diagnostic.Message,
			diagnostic.Raw,
			review.Location{Path: diagnostic.Path, StartLine: diagnostic.Line},
		))
	}
	scanLines(execution.Stdout, consumeText)
	scanLines(execution.Stderr, consumeText)
	return findings
}

func newGoVetFinding(ruleID, message, evidence string, location review.Location) review.Finding {
	return review.Finding{
		RuleID:      ruleID,
		Title:       "go vet: " + shortText(message, 100),
		Description: "Go's vet analyzer reported a likely correctness problem.",
		Severity:    review.SeverityMedium,
		Category:    review.CategoryBug,
		Location:    location,
		Evidence:    evidence,
		Suggestion:  "Fix the diagnostic and rerun go vet for the affected package.",
		Confidence:  0.98,
		Source:      NameGoVet,
	}
}
