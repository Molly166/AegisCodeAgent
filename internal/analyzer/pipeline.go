package analyzer

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/Molly166/AegisCodeAgent/internal/review"
)

const defaultAnalyzerTimeout = 2 * time.Minute

type Pipeline struct {
	analyzers map[string]Analyzer
}

type RunOptions struct {
	OnlyChangedLines bool
	AnalyzerTimeout  time.Duration
}

type Output struct {
	Analysis review.Analysis
	Findings []review.Finding
}

func NewPipeline(analyzers ...Analyzer) Pipeline {
	registered := make(map[string]Analyzer, len(analyzers))
	for _, analyzer := range analyzers {
		if analyzer != nil {
			registered[analyzer.Name()] = analyzer
		}
	}
	return Pipeline{analyzers: registered}
}

func NewDefaultPipeline(runner Runner) Pipeline {
	return NewPipeline(
		NewGoTestAnalyzer(runner),
		NewGoVetAnalyzer(runner),
		NewStaticcheckAnalyzer(runner),
		NewGosecAnalyzer(runner),
	)
}

func (p Pipeline) Run(ctx context.Context, input Input, names []string, options RunOptions) (Output, error) {
	if len(names) == 0 {
		return Output{
			Analysis: review.Analysis{
				Status:          review.AnalysisScopeOnly,
				CompletedStages: []string{"diff"},
				Tools:           []review.ToolExecution{},
			},
			Findings: []review.Finding{},
		}, nil
	}
	for _, name := range names {
		if _, ok := p.analyzers[name]; !ok {
			return Output{}, fmt.Errorf("analyzer %q is not registered", name)
		}
	}
	timeout := options.AnalyzerTimeout
	if timeout <= 0 {
		timeout = defaultAnalyzerTimeout
	}

	type analyzerRun struct {
		tool     review.ToolExecution
		findings []review.Finding
	}
	runs := make([]analyzerRun, len(names))
	done := make(chan int, len(names))
	for index, name := range names {
		go func(index int, name string) {
			started := time.Now()
			analyzerContext, cancel := context.WithTimeout(ctx, timeout)
			defer cancel()
			findings, err := p.analyzers[name].Analyze(analyzerContext, input)
			run := analyzerRun{
				tool: review.ToolExecution{
					Name:           name,
					DurationMillis: time.Since(started).Milliseconds(),
				},
				findings: findings,
			}
			classifyRun(&run.tool, findings, err)
			runs[index] = run
			done <- index
		}(index, name)
	}
	for range names {
		<-done
	}
	if err := ctx.Err(); err != nil {
		return Output{}, err
	}

	allFindings := make([]review.Finding, 0)
	tools := make([]review.ToolExecution, len(runs))
	for index, run := range runs {
		if options.OnlyChangedLines && run.tool.Status == review.ToolFindings {
			filtered := run.findings[:0]
			for _, finding := range run.findings {
				if finding.Source == NameGoTest || input.ChangedLines.Contains(finding.Location) {
					filtered = append(filtered, finding)
				}
			}
			run.tool.Suppressed = len(run.findings) - len(filtered)
			run.findings = filtered
			if len(filtered) == 0 {
				run.tool.Status = review.ToolPassed
			}
		}
		run.tool.Findings = len(run.findings)
		tools[index] = run.tool
		allFindings = append(allFindings, run.findings...)
	}

	analysis := summarizeAnalysis(tools)
	return Output{
		Analysis: analysis,
		Findings: finalizeFindings(input.Repository, allFindings),
	}, nil
}

func classifyRun(tool *review.ToolExecution, findings []review.Finding, err error) {
	if err == nil {
		if len(findings) > 0 {
			tool.Status = review.ToolFindings
		} else {
			tool.Status = review.ToolPassed
		}
		return
	}
	var skipError *SkipError
	if errors.As(err, &skipError) {
		if skipError.Kind == SkipUnavailable {
			tool.Status = review.ToolUnavailable
		} else {
			tool.Status = review.ToolSkipped
		}
		tool.Detail = skipError.Reason
		return
	}
	if errors.Is(err, context.DeadlineExceeded) {
		tool.Status = review.ToolTimedOut
		tool.Detail = "analyzer exceeded its time limit"
		return
	}
	tool.Status = review.ToolFailed
	tool.Detail = err.Error()
}

func summarizeAnalysis(tools []review.ToolExecution) review.Analysis {
	analysis := review.Analysis{
		Status:          review.AnalysisComplete,
		CompletedStages: []string{"diff", "static-analysis"},
		Tools:           tools,
	}
	successes := 0
	hardFailures := 0
	for _, tool := range tools {
		switch tool.Status {
		case review.ToolPassed, review.ToolFindings, review.ToolSkipped:
			successes++
		case review.ToolUnavailable, review.ToolFailed, review.ToolTimedOut:
			hardFailures++
		}
	}
	if hardFailures == 0 {
		return analysis
	}
	if successes == 0 {
		analysis.Status = review.AnalysisFailed
		analysis.CompletedStages = []string{"diff"}
		return analysis
	}
	analysis.Status = review.AnalysisPartial
	analysis.CompletedStages = []string{"diff", "static-analysis-partial"}
	return analysis
}

func finalizeFindings(repository string, findings []review.Finding) []review.Finding {
	unique := make(map[string]review.Finding, len(findings))
	for _, finding := range findings {
		finding.Location.Path = normalizePath(repository, finding.Location.Path)
		if finding.Confidence < 0 {
			finding.Confidence = 0
		}
		if finding.Confidence > 1 {
			finding.Confidence = 1
		}
		fingerprintInput := strings.Join([]string{
			finding.Source,
			finding.RuleID,
			finding.Location.Path,
			fmt.Sprintf("%d", finding.Location.StartLine),
			strings.ToLower(strings.Join(strings.Fields(finding.Title), " ")),
		}, "|")
		fingerprint := fmt.Sprintf("%x", sha256.Sum256([]byte(fingerprintInput)))
		finding.Fingerprint = fingerprint
		if finding.ID == "" {
			finding.ID = "AEGIS-" + strings.ToUpper(fingerprint[:12])
		}
		unique[fingerprint] = finding
	}
	result := make([]review.Finding, 0, len(unique))
	for _, finding := range unique {
		result = append(result, finding)
	}
	sort.Slice(result, func(i, j int) bool {
		left, right := result[i], result[j]
		if severityRank(left.Severity) != severityRank(right.Severity) {
			return severityRank(left.Severity) > severityRank(right.Severity)
		}
		if left.Location.Path != right.Location.Path {
			return left.Location.Path < right.Location.Path
		}
		if left.Location.StartLine != right.Location.StartLine {
			return left.Location.StartLine < right.Location.StartLine
		}
		return left.RuleID < right.RuleID
	})
	return result
}

func severityRank(severity review.Severity) int {
	switch severity {
	case review.SeverityCritical:
		return 5
	case review.SeverityHigh:
		return 4
	case review.SeverityMedium:
		return 3
	case review.SeverityLow:
		return 2
	case review.SeverityInfo:
		return 1
	default:
		return 0
	}
}
