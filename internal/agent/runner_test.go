package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Molly166/AegisCodeAgent/internal/review"
)

type scriptedProvider struct {
	responses []CompletionResponse
	err       error
	requests  []CompletionRequest
}

func (p *scriptedProvider) Name() string { return "scripted" }

func (p *scriptedProvider) Complete(_ context.Context, request CompletionRequest) (CompletionResponse, error) {
	p.requests = append(p.requests, request)
	if p.err != nil {
		return CompletionResponse{}, p.err
	}
	if len(p.responses) == 0 {
		return CompletionResponse{}, errors.New("script exhausted")
	}
	response := p.responses[0]
	p.responses = p.responses[1:]
	return response, nil
}

func TestRunnerExecutesToolLoopAndProducesUnverifiedCandidate(t *testing.T) {
	repository := t.TempDir()
	if err := os.WriteFile(filepath.Join(repository, "worker.go"), []byte("package sample\n\nfunc Run() { panic(\"boom\") }\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	tools, err := NewRepositoryTools(repository)
	if err != nil {
		t.Fatal(err)
	}
	provider := &scriptedProvider{responses: []CompletionResponse{
		{
			Model: "deepseek-v4-flash", FinishReason: "tool_calls",
			Message: Message{Role: "assistant", ReasoningContent: "private reasoning must not be persisted", ToolCalls: []ToolCall{{
				ID: "call-1", Name: "read_file_lines", Arguments: json.RawMessage(`{"path":"worker.go","start_line":1,"end_line":3}`),
			}}},
			Usage: review.AgentUsage{PromptTokens: 100, CompletionTokens: 20, ReasoningTokens: 10, TotalTokens: 120},
		},
		{
			Model: "deepseek-v4-flash", FinishReason: "stop",
			Message: Message{Role: "assistant", Content: `{"summary":"One crash candidate.","candidates":[{"title":"Unconditional panic crashes callers","description":"Run now always panics.","severity":"high","category":"bug","path":"worker.go","start_line":3,"end_line":3,"evidence":"The added function body calls panic unconditionally.","suggestion":"Return an error instead of panicking.","confidence":0.97,"verification":["Call Run from a focused test and assert it returns normally."]}]}`},
			Usage:   review.AgentUsage{PromptTokens: 140, CompletionTokens: 80, TotalTokens: 220},
		},
	}}
	runner := NewRunner(provider, tools)
	result, err := runner.Run(context.Background(), Config{
		Repository: repository, Model: DefaultModel, Thinking: true, ReasoningEffort: "high",
		MaxSteps: 3, MaxCandidates: 4, MaxInputBytes: 32 * 1024, MaxOutputTokens: 1024,
	}, fixtureRunInput())
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != review.AgentComplete || len(result.Candidates) != 1 || len(result.ToolCalls) != 1 {
		t.Fatalf("unexpected result: %+v", result)
	}
	if result.Usage.TotalTokens != 340 || result.Usage.ReasoningTokens != 10 {
		t.Fatalf("unexpected usage: %+v", result.Usage)
	}
	if !strings.HasPrefix(result.Candidates[0].ID, "AGENT-") || result.Candidates[0].Fingerprint == "" {
		t.Fatalf("candidate identity missing: %+v", result.Candidates[0])
	}
	if strings.Contains(strings.Join(result.Warnings, " "), "private reasoning") {
		t.Fatal("reasoning content leaked into the audit result")
	}
	if len(provider.requests) != 2 || len(provider.requests[1].Messages) < 4 {
		t.Fatalf("tool conversation was not continued: %+v", provider.requests)
	}
	assistant := provider.requests[1].Messages[2]
	if assistant.ReasoningContent == "" || len(assistant.ToolCalls) != 1 {
		t.Fatalf("thinking tool turn was not preserved for the provider: %+v", assistant)
	}
}

func TestRunnerRejectsExcessToolCallsAndContinues(t *testing.T) {
	calls := make([]ToolCall, 0, maxExecutedToolCallsPerStep+1)
	for index := 0; index < maxExecutedToolCallsPerStep+1; index++ {
		calls = append(calls, ToolCall{ID: fmt.Sprintf("call-%d", index+1), Name: "read_file_lines", Arguments: json.RawMessage(`{"path":"worker.go","start_line":1,"end_line":1}`)})
	}
	provider := &scriptedProvider{responses: []CompletionResponse{
		{FinishReason: "tool_calls", Message: Message{Role: "assistant", ToolCalls: calls}},
		{FinishReason: "stop", Message: Message{Role: "assistant", Content: `{"summary":"No credible candidate defects found.","candidates":[]}`}},
	}}
	tools := &countingTools{}
	result, err := NewRunner(provider, tools).Run(context.Background(), Config{
		Repository: "/tmp/repo", MaxSteps: 2, MaxCandidates: 2,
		MaxInputBytes: 32 * 1024, MaxOutputTokens: 1024,
	}, fixtureRunInput())
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != review.AgentComplete || tools.executed != maxExecutedToolCallsPerStep || len(result.ToolCalls) != len(calls) {
		t.Fatalf("unexpected bounded tool result: result=%+v executed=%d", result, tools.executed)
	}
	if result.ToolCalls[len(result.ToolCalls)-1].Status != review.AgentToolRejected || !strings.Contains(strings.Join(result.Warnings, " "), "exceeded the execution budget") {
		t.Fatalf("excess tool call was not audited as rejected: %+v", result)
	}
	lastMessage := provider.requests[1].Messages[len(provider.requests[1].Messages)-1]
	if lastMessage.Role != "tool" || !strings.Contains(lastMessage.Content, "request this tool again") {
		t.Fatalf("model did not receive a structured budget response: %+v", lastMessage)
	}
}

func TestRunnerSkipsDocumentationOnlyChange(t *testing.T) {
	provider := &scriptedProvider{}
	input := fixtureRunInput()
	input.Files[0].NewPath = "README.md"
	result, err := NewRunner(provider, nil).Run(context.Background(), Config{
		Repository: "/tmp/repo", MaxSteps: 2, MaxCandidates: 2,
		MaxInputBytes: 32 * 1024, MaxOutputTokens: 1024,
	}, input)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != review.AgentSkipped || result.Steps != 0 || len(provider.requests) != 0 || !strings.Contains(result.Summary, "no supported source-code changes") {
		t.Fatalf("unexpected documentation-only result: %+v requests=%d", result, len(provider.requests))
	}
}

func TestRunnerFailsClosedOnExtremeToolFanout(t *testing.T) {
	calls := make([]ToolCall, maxReturnedToolCallsPerStep+1)
	for index := range calls {
		calls[index] = ToolCall{ID: fmt.Sprintf("call-%d", index+1), Name: "search_code", Arguments: json.RawMessage(`{}`)}
	}
	provider := &scriptedProvider{responses: []CompletionResponse{{
		FinishReason: "tool_calls", Message: Message{Role: "assistant", ToolCalls: calls},
	}}}
	result, err := NewRunner(provider, &countingTools{}).Run(context.Background(), Config{
		Repository: "/tmp/repo", MaxSteps: 2, MaxCandidates: 2,
		MaxInputBytes: 32 * 1024, MaxOutputTokens: 1024,
	}, fixtureRunInput())
	if err == nil || result.Status != review.AgentFailed || !strings.Contains(err.Error(), "protocol maximum") {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}

func TestRunnerRejectsCandidateOutsideAddedLines(t *testing.T) {
	provider := &scriptedProvider{responses: []CompletionResponse{{
		FinishReason: "stop",
		Message:      Message{Role: "assistant", Content: `{"summary":"speculative","candidates":[{"title":"Wrong line","description":"description","severity":"high","category":"bug","path":"worker.go","start_line":2,"end_line":2,"evidence":"evidence","suggestion":"suggestion","confidence":0.9,"verification":["test"]}]}`},
	}}}
	tools := &fakeTools{}
	result, err := NewRunner(provider, tools).Run(context.Background(), Config{
		Repository: "/tmp/repo", Model: DefaultModel, MaxSteps: 1, MaxCandidates: 2,
		MaxInputBytes: 32 * 1024, MaxOutputTokens: 1024,
	}, fixtureRunInput())
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != review.AgentPartial || len(result.Candidates) != 0 || len(result.Warnings) == 0 {
		t.Fatalf("unexpected result: %+v", result)
	}
}

func TestRunnerFailsClosedOnMalformedJSON(t *testing.T) {
	provider := &scriptedProvider{responses: []CompletionResponse{{Message: Message{Role: "assistant", Content: `not-json`}}}}
	result, err := NewRunner(provider, &fakeTools{}).Run(context.Background(), Config{
		Repository: "/tmp/repo", MaxSteps: 1, MaxCandidates: 2,
		MaxInputBytes: 32 * 1024, MaxOutputTokens: 1024,
	}, fixtureRunInput())
	if err == nil || result.Status != review.AgentFailed {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}

func TestNormalizeConfigAppliesBounds(t *testing.T) {
	normalized, err := NormalizeConfig(Config{Repository: "/tmp/repo"})
	if err != nil {
		t.Fatal(err)
	}
	if normalized.Model != DefaultModel || normalized.MaxSteps != 6 || normalized.MaxInputBytes != 96*1024 {
		t.Fatalf("unexpected defaults: %+v", normalized)
	}
	for _, configuration := range []Config{
		{Repository: "", MaxSteps: 1},
		{Repository: "/tmp/repo", ReasoningEffort: "impossible"},
		{Repository: "/tmp/repo", MaxSteps: 13},
		{Repository: "/tmp/repo", MaxCandidates: 51},
		{Repository: "/tmp/repo", MaxInputBytes: 100},
		{Repository: "/tmp/repo", MaxOutputTokens: 100},
	} {
		if _, err := NormalizeConfig(configuration); err == nil {
			t.Fatalf("NormalizeConfig(%+v) error = nil", configuration)
		}
	}
}

func TestPromptRemovesAbsoluteRepositoryPath(t *testing.T) {
	input := fixtureRunInput()
	input.Comparison.Repository = "/Users/private/work/secret-repository"
	input.Context.Warnings = []string{"failed under /Users/private/work/secret-repository"}
	messages, _, err := buildInitialMessages(input, 32*1024)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(messages[1].Content, "/Users/private") || !strings.Contains(messages[1].Content, "secret-repository") {
		t.Fatalf("repository path was not minimized: %s", messages[1].Content)
	}
	if input.Context.Warnings[0] != "failed under /Users/private/work/secret-repository" {
		t.Fatal("prompt construction mutated the report context")
	}
}

func TestPromptOmitsSensitiveChangedFileContent(t *testing.T) {
	input := fixtureRunInput()
	input.Files = append(input.Files, review.ChangedFile{
		NewPath: ".secrets.json", Status: review.FileStatusAdded,
		Hunks: []review.Hunk{{NewStart: 1, NewLines: 1, Lines: []review.DiffLine{{Kind: review.LineAddition, NewLine: 1, Content: `{"token":"must-not-leak"}`}}}},
	})
	messages, warnings, err := buildInitialMessages(input, 32*1024)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(messages[1].Content, "must-not-leak") || len(warnings) == 0 {
		t.Fatalf("sensitive content was not omitted: warnings=%v input=%s", warnings, messages[1].Content)
	}
}

func TestPromptIncludesBoundedChangeIntentAsUntrustedEvidence(t *testing.T) {
	input := fixtureRunInput()
	input.Context.Intent = review.ChangeIntent{
		Source: "github_pull_request", Title: "Harden child isolation", Description: "Fixes #42",
		Labels: []string{"security"}, LinkedIssues: []string{"#42"},
		RepositoryGuidance: []review.IntentDocument{{Path: "AGENTS.md", Content: "Never expose credentials to child processes."}},
	}
	messages, _, err := buildInitialMessages(input, 32*1024)
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"change_intent", "Harden child isolation", "AGENTS.md", "#42"} {
		if !strings.Contains(messages[1].Content, expected) {
			t.Errorf("prompt does not contain intent field %q: %s", expected, messages[1].Content)
		}
	}
	if !strings.Contains(messages[0].Content, "Pull-request metadata") || !strings.Contains(messages[0].Content, "untrusted evidence") {
		t.Fatalf("system prompt does not state the intent trust boundary: %s", messages[0].Content)
	}
}

func TestCandidateOutputRequiresContractFields(t *testing.T) {
	for _, content := range []string{
		`{"summary":"missing candidates"}`,
		`{"candidates":[]}`,
		`{"summary":"","candidates":[]}`,
	} {
		if _, _, _, err := parseCandidateOutput(content, fixtureRunInput().Files, 3); err == nil {
			t.Fatalf("parseCandidateOutput(%s) error = nil", content)
		}
	}
}

type fakeTools struct{}

func (*fakeTools) Definitions() []ToolDefinition { return nil }
func (*fakeTools) Execute(context.Context, ToolCall) ToolResult {
	return ToolResult{Content: `{"ok":true}`, Status: review.AgentToolSucceeded}
}

type countingTools struct{ executed int }

func (*countingTools) Definitions() []ToolDefinition { return nil }
func (tools *countingTools) Execute(context.Context, ToolCall) ToolResult {
	tools.executed++
	return ToolResult{Content: `{"ok":true}`, Status: review.AgentToolSucceeded}
}

func fixtureRunInput() RunInput {
	return RunInput{
		Comparison: review.Comparison{Repository: "/tmp/repo", Base: "main", Head: "HEAD"},
		Files: []review.ChangedFile{{
			NewPath: "worker.go", Status: review.FileStatusModified, Stats: review.FileStats{Additions: 1},
			Hunks: []review.Hunk{{NewStart: 3, NewLines: 1, Lines: []review.DiffLine{{Kind: review.LineAddition, NewLine: 3, Content: `func Run() { panic("boom") }`}}}},
		}},
		Context: review.EmptyContextBundle(review.ContextComplete),
	}
}
