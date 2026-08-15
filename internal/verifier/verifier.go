package verifier

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/Molly166/AegisCodeAgent/internal/analyzer"
	"github.com/Molly166/AegisCodeAgent/internal/review"
)

const (
	defaultAnalyzerTimeout = 2 * time.Minute
	maxSourceFileBytes     = 2 * 1024 * 1024
	maxSnapshotBytes       = 4 * 1024
)

type Pipeline interface {
	Run(context.Context, analyzer.Input, []string, analyzer.RunOptions) (analyzer.Output, error)
}

type Config struct {
	Repository      string
	AnalyzerTimeout time.Duration
	AnalyzerNames   []string
}

type Input struct {
	Files    []review.ChangedFile
	Findings []review.Finding
	Agent    review.AgentRun
}

type Output struct {
	Verification     review.VerificationRun
	PromotedFindings []review.Finding
}

type Verifier struct {
	pipeline Pipeline
}

func New(pipeline Pipeline) Verifier {
	return Verifier{pipeline: pipeline}
}

type inspectedCandidate struct {
	candidate review.CandidateFinding
	result    review.CandidateVerification
	valid     bool
}

func (v Verifier) Run(ctx context.Context, configuration Config, input Input) (output Output, runErr error) {
	started := time.Now()
	verification := review.EmptyVerificationRun(review.VerificationComplete)
	verification.Summary.Candidates = len(input.Agent.Candidates)
	output.Verification = verification
	output.PromotedFindings = []review.Finding{}
	defer func() {
		output.Verification.DurationMillis = time.Since(started).Milliseconds()
	}()

	repository := strings.TrimSpace(configuration.Repository)
	if repository == "" {
		err := errors.New("verifier repository is required")
		output.Verification.Status = review.VerificationFailed
		output.Verification.Warnings = append(output.Verification.Warnings, err.Error())
		return output, err
	}
	resolvedRepository, err := filepath.EvalSymlinks(repository)
	if err != nil {
		wrapped := fmt.Errorf("resolve verifier repository: %w", err)
		output.Verification.Status = review.VerificationFailed
		output.Verification.Warnings = append(output.Verification.Warnings, wrapped.Error())
		return output, wrapped
	}
	resolvedRepository, err = filepath.Abs(resolvedRepository)
	if err != nil {
		wrapped := fmt.Errorf("make verifier repository absolute: %w", err)
		output.Verification.Status = review.VerificationFailed
		output.Verification.Warnings = append(output.Verification.Warnings, wrapped.Error())
		return output, wrapped
	}
	if configuration.AnalyzerTimeout <= 0 {
		configuration.AnalyzerTimeout = defaultAnalyzerTimeout
	}
	if len(configuration.AnalyzerNames) == 0 {
		configuration.AnalyzerNames = []string{analyzer.NameGoTest, analyzer.NameGoVet}
	}
	if err := ctx.Err(); err != nil {
		output.Verification.Status = review.VerificationFailed
		output.Verification.Warnings = append(output.Verification.Warnings, err.Error())
		return output, err
	}

	changedLines := analyzer.BuildChangedLineSet(resolvedRepository, input.Files)
	semanticFindings, semanticWarnings := detectSemanticFindings(resolvedRepository, input.Files, changedLines)
	output.Verification.Warnings = append(output.Verification.Warnings, semanticWarnings...)
	output.Verification.Summary.SemanticFindings = len(semanticFindings)
	if len(semanticWarnings) > 0 {
		output.Verification.Status = review.VerificationPartial
	}
	if len(input.Agent.Candidates) == 0 {
		output.PromotedFindings = append(output.PromotedFindings, semanticFindings...)
		output.Verification.Summary.Promoted = len(output.PromotedFindings)
		if input.Agent.Status == review.AgentPartial {
			output.Verification.Status = review.VerificationPartial
			output.Verification.Warnings = append(output.Verification.Warnings, "reasoning agent was partial")
		}
		output.Verification.Warnings = uniqueStrings(output.Verification.Warnings)
		return output, nil
	}
	inspected := make([]inspectedCandidate, 0, len(input.Agent.Candidates))
	validPaths := make(map[string]struct{})
	for _, candidate := range input.Agent.Candidates {
		item := inspectCandidate(resolvedRepository, changedLines, candidate)
		if item.valid && strings.EqualFold(filepath.Ext(candidate.Location.Path), ".go") {
			validPaths[normalizePath(candidate.Location.Path)] = struct{}{}
		}
		inspected = append(inspected, item)
	}

	focusedFiles := selectFiles(input.Files, validPaths)
	focusedFindings := append([]review.Finding(nil), semanticFindings...)
	if len(focusedFiles) > 0 {
		packages, packageErr := analyzer.SelectPackages(resolvedRepository, focusedFiles, analyzer.ScopeChanged)
		if packageErr != nil {
			output.Verification.Status = review.VerificationPartial
			output.Verification.Warnings = append(output.Verification.Warnings, fmt.Sprintf("select focused verification packages: %v", packageErr))
		} else if len(packages) > 0 {
			if v.pipeline == nil {
				output.Verification.Status = review.VerificationPartial
				output.Verification.Warnings = append(output.Verification.Warnings, "focused analyzer pipeline is unavailable")
			} else {
				focusedOutput, focusedErr := v.pipeline.Run(ctx, analyzer.Input{
					Repository:   resolvedRepository,
					Packages:     packages,
					ChangedLines: analyzer.BuildChangedLineSet(resolvedRepository, focusedFiles),
				}, configuration.AnalyzerNames, analyzer.RunOptions{
					OnlyChangedLines: false, AnalyzerTimeout: configuration.AnalyzerTimeout,
				})
				if focusedErr != nil {
					if ctx.Err() != nil {
						output.Verification.Status = review.VerificationFailed
						output.Verification.Warnings = append(output.Verification.Warnings, focusedErr.Error())
						return output, focusedErr
					}
					output.Verification.Status = review.VerificationPartial
					output.Verification.Warnings = append(output.Verification.Warnings, fmt.Sprintf("focused analyzer pipeline: %v", focusedErr))
				} else {
					output.Verification.Tools = append(output.Verification.Tools, focusedOutput.Analysis.Tools...)
					focusedFindings = append(focusedFindings, focusedOutput.Findings...)
					if focusedOutput.Analysis.Status == review.AnalysisPartial || focusedOutput.Analysis.Status == review.AnalysisFailed {
						output.Verification.Status = review.VerificationPartial
						output.Verification.Warnings = append(output.Verification.Warnings, "one or more focused verification analyzers did not complete")
					}
				}
			}
		}
	}

	usedSemantic := make(map[string]struct{})
	for index := range inspected {
		item := &inspected[index]
		if !item.valid {
			output.Verification.Candidates = append(output.Verification.Candidates, item.result)
			continue
		}
		existingMatches := findCorroborating(item.candidate, input.Findings)
		focusedMatches := findCorroborating(item.candidate, focusedFindings)
		for _, match := range focusedMatches {
			if strings.HasPrefix(match.Source, "semantic:") {
				usedSemantic[match.Fingerprint] = struct{}{}
			}
		}
		matches := append(append([]review.Finding(nil), existingMatches...), focusedMatches...)
		if len(matches) == 0 {
			item.result.Verdict = review.CandidateNeedsReview
			item.result.Reason = "source and diff checks passed, but available deterministic and semantic evidence could not confirm or reject this hypothesis; human review is required"
			item.result.CalibratedConfidence = clamp(item.candidate.Confidence*0.65, 0, 0.69)
			item.result.Checks = append(item.result.Checks, review.VerificationCheck{
				Name: "independent-corroboration", Status: review.VerificationCheckWarning,
				Detail: fmt.Sprintf("%s, semantic rules, and existing findings did not produce matching evidence", strings.Join(configuration.AnalyzerNames, ", ")),
			})
			output.Verification.Candidates = append(output.Verification.Candidates, item.result)
			continue
		}

		item.result.Verdict = review.CandidateVerified
		item.result.Reason = "an independent deterministic diagnostic overlaps the candidate location and shares a defect signal"
		item.result.CalibratedConfidence = calibratedConfidence(item.candidate.Confidence, matches)
		item.result.MatchedFindingIDs = findingIDs(matches)
		item.result.Checks = append(item.result.Checks, review.VerificationCheck{
			Name: "deterministic-corroboration", Status: review.VerificationCheckPassed,
			Detail: corroborationDetail(matches),
		})
		if len(existingMatches) > 0 {
			item.result.FindingID = findingIdentity(existingMatches[0])
			item.result.Promoted = false
		} else {
			promoted := promoteCandidate(item.candidate, item.result.SourceSnapshot, focusedMatches, item.result.CalibratedConfidence)
			item.result.FindingID = promoted.ID
			item.result.Promoted = true
			output.PromotedFindings = append(output.PromotedFindings, promoted)
		}
		output.Verification.Candidates = append(output.Verification.Candidates, item.result)
	}
	for _, finding := range semanticFindings {
		if _, used := usedSemantic[finding.Fingerprint]; used {
			continue
		}
		output.PromotedFindings = append(output.PromotedFindings, finding)
	}

	if input.Agent.Status == review.AgentPartial && output.Verification.Status == review.VerificationComplete {
		output.Verification.Status = review.VerificationPartial
		output.Verification.Warnings = append(output.Verification.Warnings, "reasoning agent was partial; only returned candidates were verified")
	}
	output.Verification.Warnings = uniqueStrings(output.Verification.Warnings)
	output.Verification.Summary = summarize(output.Verification.Candidates)
	output.Verification.Summary.SemanticFindings = len(semanticFindings)
	output.Verification.Summary.Promoted = len(output.PromotedFindings)
	return output, nil
}

func inspectCandidate(repository string, changedLines analyzer.ChangedLineSet, candidate review.CandidateFinding) inspectedCandidate {
	result := review.CandidateVerification{
		CandidateID: candidate.ID, Title: candidate.Title, Severity: candidate.Severity,
		Location: candidate.Location, Verdict: review.CandidateRejected,
		Checks: []review.VerificationCheck{}, MatchedFindingIDs: []string{},
	}
	item := inspectedCandidate{candidate: candidate, result: result}

	expectedFingerprint := candidateFingerprint(candidate)
	expectedID := ""
	if len(expectedFingerprint) >= 10 {
		expectedID = "AGENT-" + strings.ToUpper(expectedFingerprint[:10])
	}
	if candidate.Fingerprint == "" || candidate.Fingerprint != expectedFingerprint || candidate.ID != expectedID {
		item.result.Reason = "candidate ID does not match its canonical deduplication fingerprint"
		item.result.Checks = append(item.result.Checks, review.VerificationCheck{
			Name: "candidate-identity", Status: review.VerificationCheckFailed, Detail: item.result.Reason,
		})
		return item
	}
	item.result.Checks = append(item.result.Checks, review.VerificationCheck{
		Name: "candidate-identity", Status: review.VerificationCheckPassed, Detail: "candidate ID matches its canonical deduplication fingerprint",
	})

	if candidate.Location.Path == "" || candidate.Location.StartLine < 1 || candidate.Location.EndLine < candidate.Location.StartLine || candidate.Location.EndLine-candidate.Location.StartLine > 30 {
		item.result.Reason = "candidate location is invalid"
		item.result.Checks = append(item.result.Checks, review.VerificationCheck{
			Name: "changed-line", Status: review.VerificationCheckFailed, Detail: item.result.Reason,
		})
		return item
	}
	if !changedLines.Contains(candidate.Location) {
		item.result.Reason = "candidate no longer points to an added line in the reviewed diff"
		item.result.Checks = append(item.result.Checks, review.VerificationCheck{
			Name: "changed-line", Status: review.VerificationCheckFailed, Detail: item.result.Reason,
		})
		return item
	}
	item.result.Checks = append(item.result.Checks, review.VerificationCheck{
		Name: "changed-line", Status: review.VerificationCheckPassed, Detail: "candidate overlaps an added line in the reviewed diff",
	})

	snapshot, err := readSourceSnapshot(repository, candidate.Location)
	if err != nil {
		item.result.Reason = fmt.Sprintf("source location could not be reproduced: %v", err)
		item.result.Checks = append(item.result.Checks, review.VerificationCheck{
			Name: "source-snapshot", Status: review.VerificationCheckFailed, Detail: item.result.Reason,
		})
		return item
	}
	item.result.SourceSnapshot = snapshot
	item.result.Checks = append(item.result.Checks, review.VerificationCheck{
		Name: "source-snapshot", Status: review.VerificationCheckPassed, Detail: "source range exists in the exact analysis worktree",
	})
	if len(candidate.Verification) > 0 {
		item.result.Checks = append(item.result.Checks, review.VerificationCheck{
			Name: "model-verification-plan", Status: review.VerificationCheckWarning,
			Detail: fmt.Sprintf("%d model-proposed step(s) were retained as advisory text and never executed as commands", len(candidate.Verification)),
		})
	}
	item.valid = true
	return item
}

func readSourceSnapshot(repository string, location review.Location) (string, error) {
	path := normalizePath(location.Path)
	if !safeRelativePath(path) || !allowedSourcePath(path) {
		return "", errors.New("path is outside the verifier source allowlist")
	}
	joined := filepath.Join(repository, filepath.FromSlash(path))
	resolved, err := filepath.EvalSymlinks(joined)
	if err != nil {
		return "", err
	}
	relative, err := filepath.Rel(repository, resolved)
	if err != nil || !safeRelativePath(relative) || !allowedSourcePath(relative) {
		return "", errors.New("resolved source path escapes the repository")
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() || info.Size() > maxSourceFileBytes {
		return "", errors.New("source must be a bounded regular file")
	}
	file, err := os.Open(resolved)
	if err != nil {
		return "", err
	}
	defer file.Close()

	var builder strings.Builder
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 16*1024), maxSourceFileBytes)
	lineNumber := 0
	for scanner.Scan() {
		lineNumber++
		if lineNumber < location.StartLine {
			continue
		}
		if lineNumber > location.EndLine {
			break
		}
		fmt.Fprintf(&builder, "%d | %s\n", lineNumber, truncateUTF8(scanner.Text(), 800))
		if builder.Len() > maxSnapshotBytes {
			break
		}
	}
	if err := scanner.Err(); err != nil {
		return "", err
	}
	if lineNumber < location.StartLine || builder.Len() == 0 {
		return "", errors.New("source line does not exist")
	}
	return truncateUTF8(strings.TrimSpace(builder.String()), maxSnapshotBytes), nil
}

func findCorroborating(candidate review.CandidateFinding, findings []review.Finding) []review.Finding {
	matches := make([]review.Finding, 0)
	for _, finding := range findings {
		if normalizePath(candidate.Location.Path) != normalizePath(finding.Location.Path) || !locationsOverlap(candidate.Location, finding.Location) {
			continue
		}
		if !categoriesCompatible(candidate.Category, finding.Category) {
			continue
		}
		shared := sharedSignals(
			candidate.Title+" "+candidate.Description+" "+candidate.Evidence,
			finding.Title+" "+finding.Description+" "+finding.Evidence+" "+finding.RuleID,
		)
		if len(shared) == 0 && !(candidate.Category == review.CategoryTesting && finding.Source == analyzer.NameGoTest) {
			continue
		}
		matches = append(matches, finding)
	}
	sort.Slice(matches, func(i, j int) bool {
		if matches[i].Confidence != matches[j].Confidence {
			return matches[i].Confidence > matches[j].Confidence
		}
		return findingIdentity(matches[i]) < findingIdentity(matches[j])
	})
	return matches
}

func promoteCandidate(candidate review.CandidateFinding, snapshot string, matches []review.Finding, confidence float64) review.Finding {
	digest := sha256.Sum256([]byte("verified|" + candidate.Fingerprint))
	fingerprint := hex.EncodeToString(digest[:])
	strongest := matches[0]
	evidence := strings.TrimSpace(candidate.Evidence) + "\n\nVerifier corroboration: " + strongest.Source + "/" + strongest.RuleID + " — " + strongest.Title
	if snapshot != "" {
		evidence += "\n\nVerified source snapshot:\n" + snapshot
	}
	return review.Finding{
		ID: "AEGIS-V-" + strings.ToUpper(fingerprint[:12]), RuleID: "agent-verified:" + strongest.RuleID,
		Title: candidate.Title, Description: candidate.Description,
		Severity: conservativeSeverity(candidate.Severity, matches), Category: candidate.Category,
		Location: candidate.Location, Evidence: truncateUTF8(evidence, 6000), Suggestion: candidate.Suggestion,
		Confidence: confidence, Source: "verifier:" + strongest.Source, Fingerprint: fingerprint,
	}
}

func MergeFindings(existing, promoted []review.Finding) []review.Finding {
	seen := make(map[string]struct{}, len(existing)+len(promoted))
	result := make([]review.Finding, 0, len(existing)+len(promoted))
	for _, collection := range [][]review.Finding{existing, promoted} {
		for _, finding := range collection {
			key := finding.Fingerprint
			if key == "" {
				key = findingIdentity(finding)
			}
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			result = append(result, finding)
		}
	}
	sort.SliceStable(result, func(i, j int) bool {
		left, right := severityRank(result[i].Severity), severityRank(result[j].Severity)
		if left != right {
			return left > right
		}
		if result[i].Location.Path != result[j].Location.Path {
			return result[i].Location.Path < result[j].Location.Path
		}
		return result[i].Location.StartLine < result[j].Location.StartLine
	})
	return result
}

func selectFiles(files []review.ChangedFile, paths map[string]struct{}) []review.ChangedFile {
	selected := make([]review.ChangedFile, 0, len(paths))
	for _, file := range files {
		if _, ok := paths[normalizePath(file.NewPath)]; ok {
			selected = append(selected, file)
		}
	}
	return selected
}

func candidateFingerprint(candidate review.CandidateFinding) string {
	path := normalizePath(candidate.Location.Path)
	value := strings.Join([]string{path, fmt.Sprint(candidate.Location.StartLine), strings.ToLower(strings.TrimSpace(candidate.Title))}, "|")
	digest := sha256.Sum256([]byte(value))
	return hex.EncodeToString(digest[:])
}

func locationsOverlap(left, right review.Location) bool {
	if left.StartLine <= 0 || right.StartLine <= 0 {
		return false
	}
	leftEnd := left.EndLine
	if leftEnd < left.StartLine {
		leftEnd = left.StartLine
	}
	rightEnd := right.EndLine
	if rightEnd < right.StartLine {
		rightEnd = right.StartLine
	}
	return left.StartLine <= rightEnd && right.StartLine <= leftEnd
}

func categoriesCompatible(candidate, finding review.Category) bool {
	if candidate == finding {
		return true
	}
	return (candidate == review.CategoryBug && finding == review.CategoryTesting) ||
		(candidate == review.CategoryTesting && finding == review.CategoryBug)
}

var stopSignals = map[string]struct{}{
	"this": {}, "that": {}, "with": {}, "from": {}, "into": {}, "when": {}, "then": {},
	"changed": {}, "change": {}, "candidate": {}, "finding": {}, "error": {}, "code": {},
	"function": {}, "value": {}, "reported": {}, "likely": {}, "before": {}, "after": {},
}

func sharedSignals(left, right string) []string {
	leftTokens := signalTokens(left)
	rightTokens := signalTokens(right)
	shared := make([]string, 0)
	for token := range leftTokens {
		if _, ok := rightTokens[token]; ok {
			shared = append(shared, token)
		}
	}
	sort.Strings(shared)
	return shared
}

func signalTokens(value string) map[string]struct{} {
	fields := strings.FieldsFunc(strings.ToLower(value), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_'
	})
	result := make(map[string]struct{})
	for _, field := range fields {
		if utf8.RuneCountInString(field) < 4 {
			continue
		}
		if _, stop := stopSignals[field]; stop {
			continue
		}
		result[field] = struct{}{}
	}
	return result
}

func calibratedConfidence(candidate float64, matches []review.Finding) float64 {
	confidence := clamp(candidate, 0, 1)
	for _, finding := range matches {
		confidence = 1 - (1-confidence)*(1-clamp(finding.Confidence, 0, 1))
	}
	return clamp(confidence, 0.5, 0.99)
}

func conservativeSeverity(candidate review.Severity, matches []review.Finding) review.Severity {
	result := candidate
	strongest := review.SeverityInfo
	for _, finding := range matches {
		if severityRank(finding.Severity) > severityRank(strongest) {
			strongest = finding.Severity
		}
	}
	if severityRank(result) > severityRank(strongest) {
		return strongest
	}
	return result
}

func summarize(candidates []review.CandidateVerification) review.VerificationSummary {
	summary := review.VerificationSummary{Candidates: len(candidates)}
	for _, candidate := range candidates {
		switch candidate.Verdict {
		case review.CandidateVerified:
			summary.Verified++
		case review.CandidateRejected:
			summary.Rejected++
		case review.CandidateNeedsReview:
			summary.NeedsReview++
		case review.CandidateInconclusive:
			summary.Inconclusive++
		}
		if candidate.Promoted {
			summary.Promoted++
		}
	}
	return summary
}

func findingIDs(findings []review.Finding) []string {
	result := make([]string, 0, len(findings))
	seen := make(map[string]struct{})
	for _, finding := range findings {
		identity := findingIdentity(finding)
		if _, ok := seen[identity]; ok {
			continue
		}
		seen[identity] = struct{}{}
		result = append(result, identity)
	}
	sort.Strings(result)
	return result
}

func findingIdentity(finding review.Finding) string {
	if finding.ID != "" {
		return finding.ID
	}
	return strings.Join([]string{finding.Source, finding.RuleID, normalizePath(finding.Location.Path), fmt.Sprint(finding.Location.StartLine)}, ":")
}

func corroborationDetail(matches []review.Finding) string {
	parts := make([]string, 0, len(matches))
	for _, finding := range matches {
		parts = append(parts, finding.Source+"/"+finding.RuleID+" at "+normalizePath(finding.Location.Path)+":"+fmt.Sprint(finding.Location.StartLine))
	}
	return strings.Join(parts, "; ")
}

func safeRelativePath(path string) bool {
	if path == "" || filepath.IsAbs(path) {
		return false
	}
	clean := filepath.Clean(path)
	return clean != "." && clean != ".." && !strings.HasPrefix(clean, ".."+string(filepath.Separator))
}

func allowedSourcePath(path string) bool {
	for _, component := range strings.Split(filepath.ToSlash(path), "/") {
		if strings.HasPrefix(component, ".") && component != ".github" {
			return false
		}
	}
	switch strings.ToLower(filepath.Ext(path)) {
	case ".go", ".mod", ".sum", ".json", ".yaml", ".yml", ".toml", ".sql", ".proto", ".md":
		return true
	default:
		return false
	}
}

func normalizePath(path string) string {
	return strings.TrimPrefix(filepath.ToSlash(filepath.Clean(path)), "./")
}

func uniqueStrings(values []string) []string {
	seen := make(map[string]struct{})
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}

func clamp(value, minimum, maximum float64) float64 {
	if value < minimum {
		return minimum
	}
	if value > maximum {
		return maximum
	}
	return value
}

func severityRank(value review.Severity) int {
	switch value {
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

func truncateUTF8(value string, maxBytes int) string {
	if len(value) <= maxBytes {
		return value
	}
	value = value[:maxBytes]
	for !utf8.ValidString(value) {
		value = value[:len(value)-1]
	}
	return value + "…"
}
