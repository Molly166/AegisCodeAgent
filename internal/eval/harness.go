package eval

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/Molly166/AegisCodeAgent/internal/githubreport"
	"github.com/Molly166/AegisCodeAgent/internal/review"
)

const (
	maxCaseBytes   = 1024 * 1024
	maxReportBytes = 32 * 1024 * 1024
)

type Config struct {
	Corpus string
	Gate   githubreport.Options
}

func Run(configuration Config) (HarnessReport, error) {
	if configuration.Gate.FailOn == "" {
		configuration.Gate.FailOn = githubreport.PriorityP1
	}
	if configuration.Gate.FailOnNeedsReview == "" {
		configuration.Gate.FailOnNeedsReview = githubreport.PriorityP0
	}
	root, err := filepath.Abs(strings.TrimSpace(configuration.Corpus))
	if err != nil || strings.TrimSpace(configuration.Corpus) == "" {
		return HarnessReport{}, errors.New("eval corpus path is required")
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return HarnessReport{}, fmt.Errorf("resolve eval corpus: %w", err)
	}
	casePaths, err := discoverCases(root)
	if err != nil {
		return HarnessReport{}, err
	}
	if len(casePaths) == 0 {
		return HarnessReport{}, fmt.Errorf("eval corpus %q contains no case.json files", root)
	}

	result := HarnessReport{
		SchemaVersion: ReportSchemaVersion, GeneratedAt: time.Now().UTC(), Corpus: filepath.ToSlash(filepath.Clean(configuration.Corpus)),
		Gate: configuration.Gate, Cases: make([]CaseResult, 0, len(casePaths)),
	}
	seenIDs := make(map[string]struct{})
	for _, path := range casePaths {
		spec, err := loadCase(path)
		if err != nil {
			return HarnessReport{}, err
		}
		if _, duplicate := seenIDs[spec.ID]; duplicate {
			return HarnessReport{}, fmt.Errorf("duplicate eval case ID %q", spec.ID)
		}
		seenIDs[spec.ID] = struct{}{}
		if err := validateCase(spec); err != nil {
			return HarnessReport{}, fmt.Errorf("validate %s: %w", path, err)
		}
		reportPath, err := resolveCaseReport(root, filepath.Dir(path), spec.Report)
		if err != nil {
			return HarnessReport{}, fmt.Errorf("resolve report for case %s: %w", spec.ID, err)
		}
		reviewReport, err := loadReviewReport(reportPath)
		if err != nil {
			return HarnessReport{}, fmt.Errorf("load report for case %s: %w", spec.ID, err)
		}
		relativeReportPath, _ := filepath.Rel(root, reportPath)
		result.Cases = append(result.Cases, evaluateCase(spec, reviewReport, filepath.ToSlash(relativeReportPath), configuration.Gate))
	}
	result.Metrics = calculateMetrics(result.Cases)
	return result, nil
}

func discoverCases(root string) ([]string, error) {
	paths := make([]string, 0)
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.Type()&os.ModeSymlink != 0 {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if !entry.IsDir() && entry.Name() == "case.json" {
			paths = append(paths, path)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("walk eval corpus: %w", err)
	}
	sort.Strings(paths)
	return paths, nil
}

func loadCase(path string) (CaseSpec, error) {
	var spec CaseSpec
	if err := decodeBoundedJSON(path, maxCaseBytes, &spec); err != nil {
		return CaseSpec{}, fmt.Errorf("load eval case %s: %w", path, err)
	}
	return spec, nil
}

func validateCase(spec CaseSpec) error {
	if spec.SchemaVersion != CaseSchemaVersion {
		return fmt.Errorf("schema %q is incompatible with %q", spec.SchemaVersion, CaseSchemaVersion)
	}
	if strings.TrimSpace(spec.ID) == "" || strings.TrimSpace(spec.Title) == "" {
		return errors.New("id and title are required")
	}
	if strings.TrimSpace(spec.Report) == "" {
		return errors.New("report is required")
	}
	if spec.ExpectedGate != GatePassed && spec.ExpectedGate != GateBlocked {
		return fmt.Errorf("expected_gate must be %q or %q", GatePassed, GateBlocked)
	}
	if spec.MaxUnexpectedFindings < 0 || spec.ExpectedNeedsReview < 0 {
		return errors.New("max_unexpected_findings and expected_needs_review cannot be negative")
	}
	for index, finding := range spec.ExpectedFindings {
		if !validSeverity(finding.Severity) || !safeEvalPath(finding.Path) || finding.StartLine < 0 {
			return fmt.Errorf("expected_findings[%d] requires a valid severity and path", index)
		}
	}
	return nil
}

func resolveCaseReport(root, directory, relative string) (string, error) {
	if filepath.IsAbs(relative) {
		return "", errors.New("report path must be relative")
	}
	joined := filepath.Clean(filepath.Join(directory, relative))
	info, err := os.Lstat(joined)
	if err != nil {
		return "", err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return "", errors.New("report must be a regular non-symlink file")
	}
	resolved, err := filepath.EvalSymlinks(joined)
	if err != nil {
		return "", err
	}
	relativeToRoot, err := filepath.Rel(root, resolved)
	if err != nil || relativeToRoot == ".." || strings.HasPrefix(relativeToRoot, ".."+string(filepath.Separator)) {
		return "", errors.New("report path escapes the eval corpus")
	}
	return resolved, nil
}

func loadReviewReport(path string) (review.ReviewReport, error) {
	var result review.ReviewReport
	if err := decodeBoundedJSON(path, maxReportBytes, &result); err != nil {
		return review.ReviewReport{}, err
	}
	if err := review.UpgradeReport(&result); err != nil {
		return review.ReviewReport{}, err
	}
	return result, nil
}

func decodeBoundedJSON(path string, maximum int64, destination any) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Size() > maximum {
		return fmt.Errorf("file must be regular and no larger than %d bytes", maximum)
	}
	decoder := json.NewDecoder(io.LimitReader(file, maximum+1))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			err = errors.New("multiple JSON values are not allowed")
		}
		return err
	}
	return nil
}

func evaluateCase(spec CaseSpec, report review.ReviewReport, reportPath string, gateOptions githubreport.Options) CaseResult {
	result := CaseResult{
		ID: spec.ID, Title: spec.Title, Description: spec.Description, Tags: append([]string(nil), spec.Tags...),
		ExpectedGate: spec.ExpectedGate, Matches: []FindingMatch{}, Missed: []ExpectedFinding{}, Unexpected: []review.Finding{},
		ExpectedNeedsReview: spec.ExpectedNeedsReview, MaxUnexpectedFindings: spec.MaxUnexpectedFindings, ReportPath: reportPath,
		AgentTokens: report.Agent.Usage.TotalTokens, AgentDurationMillis: report.Agent.DurationMillis,
	}
	gate := githubreport.EvaluateWithOptions(report, gateOptions)
	result.ActualGate = GatePassed
	if gate.Blocked {
		result.ActualGate = GateBlocked
	}
	result.GateCorrect = result.ExpectedGate == result.ActualGate
	used := make([]bool, len(report.Findings))
	for _, expected := range spec.ExpectedFindings {
		matched := -1
		for index, actual := range report.Findings {
			if !used[index] && findingMatches(expected, actual) {
				matched = index
				break
			}
		}
		if matched < 0 {
			result.Missed = append(result.Missed, expected)
			continue
		}
		used[matched] = true
		result.Matches = append(result.Matches, FindingMatch{Expected: expected, Actual: report.Findings[matched]})
	}
	for index, actual := range report.Findings {
		if !used[index] {
			result.Unexpected = append(result.Unexpected, actual)
		}
	}
	for _, candidate := range report.Verification.Candidates {
		if candidate.Verdict == review.CandidateNeedsReview || candidate.Verdict == review.CandidateInconclusive {
			result.NeedsReview++
		}
	}
	result.Passed = result.GateCorrect && result.NeedsReview == result.ExpectedNeedsReview && len(result.Missed) == 0 && len(result.Unexpected) <= result.MaxUnexpectedFindings
	return result
}

func findingMatches(expected ExpectedFinding, actual review.Finding) bool {
	if expected.Severity != actual.Severity || filepath.ToSlash(filepath.Clean(expected.Path)) != filepath.ToSlash(filepath.Clean(actual.Location.Path)) {
		return false
	}
	if expected.Category != "" && expected.Category != actual.Category {
		return false
	}
	if expected.StartLine > 0 && expected.StartLine != actual.Location.StartLine {
		return false
	}
	if expected.RuleID != "" && expected.RuleID != actual.RuleID {
		return false
	}
	return expected.TitleContains == "" || strings.Contains(strings.ToLower(actual.Title), strings.ToLower(expected.TitleContains))
}

func calculateMetrics(cases []CaseResult) Metrics {
	metrics := Metrics{Cases: len(cases)}
	for _, result := range cases {
		if result.Passed {
			metrics.PassedCases++
		}
		metrics.ExpectedFindings += len(result.Matches) + len(result.Missed)
		metrics.ActualFindings += len(result.Matches) + len(result.Unexpected)
		metrics.MatchedFindings += len(result.Matches)
		metrics.FalsePositives += len(result.Unexpected)
		metrics.FalseNegatives += len(result.Missed)
		metrics.UnresolvedHypotheses += result.NeedsReview
		metrics.AgentTokens += result.AgentTokens
		metrics.AgentDurationMillis += result.AgentDurationMillis
		if result.GateCorrect {
			metrics.GateCorrect++
		}
		if len(result.Matches)+len(result.Missed) == 0 && result.ExpectedGate == GatePassed {
			metrics.CleanCases++
			if result.ActualGate == GateBlocked {
				metrics.FalseBlocks++
			}
		}
		for _, match := range result.Matches {
			incrementPriorityMetric(&metrics, match.Expected.Severity, true)
		}
		for _, missed := range result.Missed {
			incrementPriorityMetric(&metrics, missed.Severity, false)
		}
	}
	metrics.Precision = ratio(metrics.MatchedFindings, metrics.MatchedFindings+metrics.FalsePositives)
	metrics.Recall = ratio(metrics.MatchedFindings, metrics.MatchedFindings+metrics.FalseNegatives)
	if metrics.Precision+metrics.Recall > 0 {
		metrics.F1 = 2 * metrics.Precision * metrics.Recall / (metrics.Precision + metrics.Recall)
	}
	metrics.P0Recall = ratio(metrics.P0Matched, metrics.P0Expected)
	metrics.P1Recall = ratio(metrics.P1Matched, metrics.P1Expected)
	metrics.GateAccuracy = ratio(metrics.GateCorrect, metrics.Cases)
	metrics.FalseBlockRate = ratio(metrics.FalseBlocks, metrics.CleanCases)
	return metrics
}

func safeEvalPath(path string) bool {
	path = filepath.Clean(strings.TrimSpace(path))
	return path != "" && path != "." && path != ".." && !filepath.IsAbs(path) && !strings.HasPrefix(path, ".."+string(filepath.Separator))
}

func incrementPriorityMetric(metrics *Metrics, severity review.Severity, matched bool) {
	switch githubreport.PriorityForSeverity(severity) {
	case githubreport.PriorityP0:
		metrics.P0Expected++
		if matched {
			metrics.P0Matched++
		}
	case githubreport.PriorityP1:
		metrics.P1Expected++
		if matched {
			metrics.P1Matched++
		}
	}
}

func ratio(numerator, denominator int) float64 {
	if denominator == 0 {
		return 1
	}
	return float64(numerator) / float64(denominator)
}

func validSeverity(severity review.Severity) bool {
	switch severity {
	case review.SeverityCritical, review.SeverityHigh, review.SeverityMedium, review.SeverityLow, review.SeverityInfo:
		return true
	default:
		return false
	}
}
