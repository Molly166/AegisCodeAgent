package agent

import (
	"context"
	"encoding/json"
	"time"

	"github.com/Molly166/AegisCodeAgent/internal/review"
)

const (
	ProviderNone     = "none"
	ProviderDeepSeek = "deepseek"
	DefaultModel     = "deepseek-v4-flash"
)

type Config struct {
	Repository      string
	Model           string
	Thinking        bool
	ReasoningEffort string
	MaxSteps        int
	MaxCandidates   int
	MaxInputBytes   int
	MaxOutputTokens int
}

type RunInput struct {
	Comparison     review.Comparison
	Files          []review.ChangedFile
	StaticFindings []review.Finding
	Context        review.ContextBundle
}

type Provider interface {
	Name() string
	Complete(ctx context.Context, request CompletionRequest) (CompletionResponse, error)
}

type CompletionRequest struct {
	Model           string
	Messages        []Message
	Tools           []ToolDefinition
	JSONOutput      bool
	Thinking        bool
	ReasoningEffort string
	MaxOutputTokens int
}

type CompletionResponse struct {
	Model        string
	Message      Message
	FinishReason string
	Usage        review.AgentUsage
}

type Message struct {
	Role             string
	Content          string
	ReasoningContent string
	ToolCalls        []ToolCall
	ToolCallID       string
}

type ToolCall struct {
	ID        string
	Name      string
	Arguments json.RawMessage
}

type ToolDefinition struct {
	Name        string
	Description string
	Parameters  map[string]any
}

type ToolExecutor interface {
	Definitions() []ToolDefinition
	Execute(ctx context.Context, call ToolCall) ToolResult
}

type ToolResult struct {
	Content  string
	Status   review.AgentToolStatus
	Summary  string
	Duration time.Duration
}
