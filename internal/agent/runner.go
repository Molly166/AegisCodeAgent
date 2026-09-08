package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/Molly166/AegisCodeAgent/internal/review"
)

const (
	maxExecutedToolCallsPerStep = 4
	maxReturnedToolCallsPerStep = 16
)

type Runner struct {
	provider Provider
	tools    ToolExecutor
}

func NewRunner(provider Provider, tools ToolExecutor) Runner {
	return Runner{provider: provider, tools: tools}
}

func ValidateProvider(name string) error {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case ProviderNone, ProviderDeepSeek, ProviderOrcaRouter, ProviderOpenAICompatible:
		return nil
	default:
		return fmt.Errorf("unsupported agent provider %q (supported: none, deepseek, orcarouter, openai-compatible)", name)
	}
}

func NormalizeConfig(configuration Config) (Config, error) {
	configuration.Repository = strings.TrimSpace(configuration.Repository)
	configuration.Model = strings.TrimSpace(configuration.Model)
	configuration.ReasoningEffort = strings.ToLower(strings.TrimSpace(configuration.ReasoningEffort))
	if configuration.Repository == "" {
		return Config{}, errors.New("agent repository is required")
	}
	if configuration.Model == "" {
		configuration.Model = DefaultModel
	}
	if err := validateModel(configuration.Model); err != nil {
		return Config{}, err
	}
	if configuration.ReasoningEffort == "" {
		configuration.ReasoningEffort = "high"
	}
	switch configuration.ReasoningEffort {
	case "minimal", "low", "medium", "high", "xhigh", "max":
	default:
		return Config{}, fmt.Errorf("unsupported reasoning effort %q", configuration.ReasoningEffort)
	}
	if configuration.MaxSteps == 0 {
		configuration.MaxSteps = 6
	}
	if configuration.MaxCandidates == 0 {
		configuration.MaxCandidates = 12
	}
	if configuration.MaxInputBytes == 0 {
		configuration.MaxInputBytes = 96 * 1024
	}
	if configuration.MaxOutputTokens == 0 {
		configuration.MaxOutputTokens = 8192
	}
	if configuration.MaxSteps < 1 || configuration.MaxSteps > 12 {
		return Config{}, errors.New("agent max steps must be between 1 and 12")
	}
	if configuration.MaxCandidates < 1 || configuration.MaxCandidates > 50 {
		return Config{}, errors.New("agent max candidates must be between 1 and 50")
	}
	if configuration.MaxInputBytes < 16*1024 || configuration.MaxInputBytes > 2*1024*1024 {
		return Config{}, errors.New("agent max input bytes must be between 16384 and 2097152")
	}
	if configuration.MaxOutputTokens < 512 || configuration.MaxOutputTokens > 65536 {
		return Config{}, errors.New("agent max output tokens must be between 512 and 65536")
	}
	return configuration, nil
}

func (r Runner) Run(ctx context.Context, configuration Config, input RunInput) (result review.AgentRun, runErr error) {
	started := time.Now()
	result = review.EmptyAgentRun(review.AgentFailed)
	if r.provider != nil {
		result.Provider = r.provider.Name()
	}
	result.Model = configuration.Model
	result.RequestedModel = configuration.Model
	result.Thinking = configuration.Thinking
	defer func() { result.DurationMillis = time.Since(started).Milliseconds() }()

	configuration, err := NormalizeConfig(configuration)
	if err != nil {
		result.Warnings = append(result.Warnings, err.Error())
		return result, err
	}
	result.Model = configuration.Model
	result.RequestedModel = configuration.Model
	if !hasReviewableSourceChange(input.Files) {
		result.Status = review.AgentSkipped
		result.Summary = "Reasoning was skipped because this comparison contains no supported source-code changes."
		return result, nil
	}
	if r.provider == nil {
		err := errors.New("agent provider is required")
		result.Warnings = append(result.Warnings, err.Error())
		return result, err
	}
	if r.tools == nil {
		err := errors.New("agent tool executor is required")
		result.Warnings = append(result.Warnings, err.Error())
		return result, err
	}

	definitions := r.tools.Definitions()
	jsonOutput := true
	toolCallingEnabled := true
	replayReasoning := true
	if provider, ok := r.provider.(CapabilityProvider); ok {
		capabilities := provider.Capabilities(configuration.Model)
		replayReasoning = capabilities.ReplayReasoning
		if !capabilities.ToolCalling {
			toolCallingEnabled = false
			definitions = nil
			result.Warnings = append(result.Warnings, "tool calling is disabled for the selected model; review uses only the supplied diff and repository context")
		}
		if !capabilities.JSONOutput {
			jsonOutput = false
			result.Warnings = append(result.Warnings, "JSON mode is disabled for the selected model; final output is still strictly validated against the candidate schema")
		}
	}
	messages, warnings, err := buildBudgetedInitialMessages(input, configuration.MaxInputBytes, definitions, replayReasoning)
	result.Warnings = append(result.Warnings, warnings...)
	if err != nil {
		result.Warnings = append(result.Warnings, err.Error())
		return result, err
	}
	outputTruncated := false
	for step := 1; step <= configuration.MaxSteps; step++ {
		if err := ctx.Err(); err != nil {
			result.Warnings = append(result.Warnings, err.Error())
			return result, err
		}
		// Bound cumulative conversation growth as well as the initial diff. Tools
		// and reasoning from earlier turns must not silently exhaust the budget.
		if conversationInputBytes(messages, definitions, replayReasoning) > configuration.MaxInputBytes {
			result.Status = review.AgentPartial
			result.Warnings = append(result.Warnings, "agent stopped because the cumulative conversation exceeds max-input-bytes")
			return result, nil
		}
		completionStarted := time.Now()
		response, err := r.provider.Complete(ctx, CompletionRequest{
			Model: configuration.Model, Messages: messages, Tools: definitions,
			JSONOutput: jsonOutput, Thinking: configuration.Thinking,
			ReasoningEffort: configuration.ReasoningEffort,
			MaxOutputTokens: configuration.MaxOutputTokens,
		})
		result.Steps = step
		metadata := response.Metadata
		if metadata.Provider == "" {
			metadata.Provider = r.provider.Name()
		}
		if metadata.RequestedModel == "" {
			metadata.RequestedModel = configuration.Model
		}
		if metadata.DurationMillis == 0 {
			metadata.DurationMillis = time.Since(completionStarted).Milliseconds()
		}
		if metadata.ResolvedModel == "" {
			metadata.ResolvedModel = response.Model
		}
		if err != nil && metadata.ErrorKind == "" {
			metadata.ErrorKind = "completion_failed"
		}
		result.Completions = append(result.Completions, review.AgentCompletionTrace{Step: step, CompletionMetadata: metadata})
		if err != nil {
			wrapped := fmt.Errorf("%s completion step %d: %w", r.provider.Name(), step, err)
			result.Warnings = append(result.Warnings, wrapped.Error())
			return result, wrapped
		}
		addUsage(&result.Usage, response.Usage)
		if response.Model != "" {
			result.Model = response.Model
		}
		messages = append(messages, response.Message)
		if response.FinishReason == "length" {
			outputTruncated = true
			result.Warnings = append(result.Warnings, fmt.Sprintf("model output reached the token limit at step %d; coverage may be incomplete", step))
		}

		if len(response.Message.ToolCalls) > 0 {
			if !toolCallingEnabled {
				err := errors.New("model returned tool calls while tool calling is disabled")
				result.Warnings = append(result.Warnings, err.Error())
				return result, err
			}
			if len(response.Message.ToolCalls) > maxReturnedToolCallsPerStep {
				err := fmt.Errorf("model returned %d tool calls in one step; protocol maximum is %d", len(response.Message.ToolCalls), maxReturnedToolCallsPerStep)
				result.Warnings = append(result.Warnings, err.Error())
				return result, err
			}
			if len(response.Message.ToolCalls) > maxExecutedToolCallsPerStep {
				result.Warnings = append(result.Warnings, fmt.Sprintf(
					"step %d requested %d tool calls; %d exceeded the execution budget and were returned to the model as rejected",
					step, len(response.Message.ToolCalls), len(response.Message.ToolCalls)-maxExecutedToolCallsPerStep,
				))
			}
			for index, call := range response.Message.ToolCalls {
				if strings.TrimSpace(call.ID) == "" {
					err := errors.New("model returned a tool call without an ID")
					result.Warnings = append(result.Warnings, err.Error())
					return result, err
				}
				toolResult := ToolResult{}
				if index < maxExecutedToolCallsPerStep {
					toolResult = r.tools.Execute(ctx, call)
				} else {
					detail := fmt.Sprintf("per-step tool execution budget is %d; request this tool again in the next turn", maxExecutedToolCallsPerStep)
					toolResult = ToolResult{
						Content: marshalToolOutput(map[string]any{"ok": false, "error": detail}),
						Status:  review.AgentToolRejected, Summary: detail,
					}
				}
				result.ToolCalls = append(result.ToolCalls, review.AgentToolExecution{
					Step: step, CallID: truncateText(call.ID, 160), Name: truncateText(call.Name, 80),
					Arguments: truncateText(string(call.Arguments), 2000), Status: toolResult.Status,
					DurationMillis: toolResult.Duration.Milliseconds(), ResultSummary: toolResult.Summary,
				})
				messages = append(messages, Message{Role: "tool", ToolCallID: call.ID, Content: toolResult.Content})
			}
			continue
		}
		if response.FinishReason != "" && response.FinishReason != "stop" && response.FinishReason != "length" {
			err := fmt.Errorf("model stopped with finish reason %q", response.FinishReason)
			result.Warnings = append(result.Warnings, err.Error())
			return result, err
		}

		candidates, summary, validationWarnings, err := parseCandidateOutput(response.Message.Content, input.Files, configuration.MaxCandidates)
		result.Warnings = append(result.Warnings, validationWarnings...)
		result.Summary = summary
		result.Candidates = candidates
		if err != nil {
			wrapped := fmt.Errorf("validate model output: %w", err)
			result.Warnings = append(result.Warnings, wrapped.Error())
			return result, wrapped
		}
		result.Status = review.AgentComplete
		if len(validationWarnings) > 0 || len(warnings) > 0 || outputTruncated {
			result.Status = review.AgentPartial
		}
		result.Warnings = uniqueStrings(result.Warnings)
		return result, nil
	}

	result.Status = review.AgentPartial
	result.Warnings = uniqueStrings(append(result.Warnings, "agent stopped after reaching the configured step limit"))
	return result, nil
}

func hasReviewableSourceChange(files []review.ChangedFile) bool {
	return review.HasReviewableSourceChange(files)
}

func addUsage(total *review.AgentUsage, value review.AgentUsage) {
	total.PromptTokens += value.PromptTokens
	total.CompletionTokens += value.CompletionTokens
	total.ReasoningTokens += value.ReasoningTokens
	total.TotalTokens += value.TotalTokens
}

type candidateEnvelope struct {
	Summary    *string         `json:"summary"`
	Candidates *[]rawCandidate `json:"candidates"`
}

type rawCandidate struct {
	Title        string          `json:"title"`
	Description  string          `json:"description"`
	Severity     review.Severity `json:"severity"`
	Category     review.Category `json:"category"`
	Path         string          `json:"path"`
	StartLine    int             `json:"start_line"`
	EndLine      int             `json:"end_line"`
	Evidence     string          `json:"evidence"`
	Suggestion   string          `json:"suggestion"`
	Confidence   *float64        `json:"confidence"`
	Verification []string        `json:"verification"`
}

func parseCandidateOutput(content string, files []review.ChangedFile, maxCandidates int) ([]review.CandidateFinding, string, []string, error) {
	if strings.TrimSpace(content) == "" {
		return nil, "", nil, errors.New("model returned empty content")
	}
	decoder := json.NewDecoder(strings.NewReader(content))
	decoder.DisallowUnknownFields()
	var envelope candidateEnvelope
	if err := decoder.Decode(&envelope); err != nil {
		return nil, "", nil, errors.New("decode candidate JSON: malformed JSON or invalid schema")
	}
	if err := ensureDecoderEOF(decoder); err != nil {
		return nil, "", nil, errors.New("decode candidate JSON: trailing content is not allowed")
	}
	if envelope.Summary == nil || strings.TrimSpace(*envelope.Summary) == "" || envelope.Candidates == nil {
		return nil, "", nil, errors.New("summary and candidates fields are required")
	}
	candidates := *envelope.Candidates
	if len(candidates) > maxCandidates {
		return nil, "", nil, fmt.Errorf("model returned %d candidates; maximum is %d", len(candidates), maxCandidates)
	}
	addedLines := changedLineIndex(files)
	seen := make(map[string]struct{})
	result := make([]review.CandidateFinding, 0, len(candidates))
	warnings := make([]string, 0)
	for index, candidate := range candidates {
		normalized, err := validateCandidate(candidate, addedLines)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("candidate %d was rejected: %v", index+1, err))
			continue
		}
		if _, ok := seen[normalized.Fingerprint]; ok {
			warnings = append(warnings, fmt.Sprintf("candidate %d was rejected as a duplicate", index+1))
			continue
		}
		seen[normalized.Fingerprint] = struct{}{}
		result = append(result, normalized)
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
	return result, truncateText(strings.TrimSpace(*envelope.Summary), 1000), warnings, nil
}

func validateCandidate(candidate rawCandidate, addedLines map[string]map[int]struct{}) (review.CandidateFinding, error) {
	title := strings.TrimSpace(candidate.Title)
	description := strings.TrimSpace(candidate.Description)
	evidence := strings.TrimSpace(candidate.Evidence)
	suggestion := strings.TrimSpace(candidate.Suggestion)
	if title == "" || description == "" || evidence == "" || suggestion == "" {
		return review.CandidateFinding{}, errors.New("title, description, evidence, and suggestion are required")
	}
	if len(title) > 180 || len(description) > 4000 || len(evidence) > 4000 || len(suggestion) > 3000 {
		return review.CandidateFinding{}, errors.New("one or more text fields exceed their size limit")
	}
	if !validSeverity(candidate.Severity) || !validCategory(candidate.Category) {
		return review.CandidateFinding{}, errors.New("severity or category is invalid")
	}
	path := filepath.ToSlash(filepath.Clean(strings.TrimSpace(candidate.Path)))
	if path == "." || filepath.IsAbs(path) || path == ".." || strings.HasPrefix(path, "../") {
		return review.CandidateFinding{}, errors.New("path must be repository-relative")
	}
	lines := addedLines[path]
	if lines == nil {
		return review.CandidateFinding{}, errors.New("path is not a changed text file")
	}
	if candidate.StartLine < 1 || candidate.EndLine < candidate.StartLine || candidate.EndLine-candidate.StartLine > 30 {
		return review.CandidateFinding{}, errors.New("location must be a positive range of at most 31 lines")
	}
	if _, ok := lines[candidate.StartLine]; !ok {
		return review.CandidateFinding{}, errors.New("start_line is not an added line in the reviewed diff")
	}
	if candidate.Confidence == nil || *candidate.Confidence < 0 || *candidate.Confidence > 1 {
		return review.CandidateFinding{}, errors.New("confidence must be between 0 and 1")
	}
	verification := make([]string, 0, len(candidate.Verification))
	for _, step := range candidate.Verification {
		step = strings.TrimSpace(step)
		if step != "" {
			verification = append(verification, truncateText(step, 600))
		}
		if len(verification) == 5 {
			break
		}
	}
	if len(verification) == 0 {
		return review.CandidateFinding{}, errors.New("at least one verification step is required")
	}
	fingerprintInput := strings.Join([]string{path, fmt.Sprint(candidate.StartLine), strings.ToLower(title)}, "|")
	digest := sha256.Sum256([]byte(fingerprintInput))
	fingerprint := hex.EncodeToString(digest[:])
	return review.CandidateFinding{
		ID: "AGENT-" + strings.ToUpper(fingerprint[:10]), Fingerprint: fingerprint,
		Title: title, Description: description, Severity: candidate.Severity, Category: candidate.Category,
		Location: review.Location{Path: path, StartLine: candidate.StartLine, EndLine: candidate.EndLine},
		Evidence: evidence, Suggestion: suggestion, Confidence: *candidate.Confidence, Verification: verification,
	}, nil
}

func changedLineIndex(files []review.ChangedFile) map[string]map[int]struct{} {
	result := make(map[string]map[int]struct{})
	for _, file := range files {
		if file.Binary || file.Status == review.FileStatusDeleted || file.NewPath == "" || !allowedToolPath(file.NewPath) || !searchableFile(file.NewPath) {
			continue
		}
		path := filepath.ToSlash(filepath.Clean(file.NewPath))
		lines := make(map[int]struct{})
		for _, hunk := range file.Hunks {
			for _, line := range hunk.Lines {
				if line.Kind == review.LineAddition && line.NewLine > 0 {
					lines[line.NewLine] = struct{}{}
				}
			}
		}
		result[path] = lines
	}
	return result
}

func validSeverity(value review.Severity) bool {
	switch value {
	case review.SeverityCritical, review.SeverityHigh, review.SeverityMedium, review.SeverityLow, review.SeverityInfo:
		return true
	default:
		return false
	}
}

func validCategory(value review.Category) bool {
	switch value {
	case review.CategoryBug, review.CategorySecurity, review.CategoryPerformance, review.CategoryMaintainability, review.CategoryTesting:
		return true
	default:
		return false
	}
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
	default:
		return 1
	}
}
