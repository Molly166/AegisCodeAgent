package review

type AgentStatus string

const (
	AgentNotRun   AgentStatus = "not_run"
	AgentSkipped  AgentStatus = "skipped"
	AgentComplete AgentStatus = "complete"
	AgentPartial  AgentStatus = "partial"
	AgentFailed   AgentStatus = "failed"
)

// AgentRun is the auditable result of the probabilistic reasoning stage.
// Candidates are deliberately kept separate from verified findings so an LLM
// response cannot change the final risk verdict before the verifier runs.
type AgentRun struct {
	Status         AgentStatus            `json:"status"`
	Provider       string                 `json:"provider,omitempty"`
	Model          string                 `json:"model,omitempty"`
	RequestedModel string                 `json:"requested_model,omitempty"`
	Thinking       bool                   `json:"thinking"`
	Steps          int                    `json:"steps"`
	DurationMillis int64                  `json:"duration_ms"`
	Summary        string                 `json:"summary,omitempty"`
	Usage          AgentUsage             `json:"usage"`
	Candidates     []CandidateFinding     `json:"candidates"`
	ToolCalls      []AgentToolExecution   `json:"tool_calls"`
	Warnings       []string               `json:"warnings"`
	Completions    []AgentCompletionTrace `json:"completions,omitempty"`
}

// CompletionMetadata contains only bounded operational metadata, never prompts,
// API credentials, response bodies, or private model reasoning. ResolvedModel is
// what the provider reports; an empty value means it could not be determined.
type CompletionMetadata struct {
	Provider       string `json:"provider"`
	RequestedModel string `json:"requested_model"`
	ResolvedModel  string `json:"resolved_model,omitempty"`
	RequestID      string `json:"request_id,omitempty"`
	FallbackModel  string `json:"fallback_model,omitempty"`
	FallbackLevel  int    `json:"fallback_level,omitempty"`
	Attempts       int    `json:"attempts"`
	DurationMillis int64  `json:"duration_ms"`
	HTTPStatus     int    `json:"http_status,omitempty"`
	ErrorKind      string `json:"error_kind,omitempty"`
}

type AgentCompletionTrace struct {
	Step int `json:"step"`
	CompletionMetadata
}

type AgentUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	ReasoningTokens  int `json:"reasoning_tokens,omitempty"`
	TotalTokens      int `json:"total_tokens"`
}

type AgentToolStatus string

const (
	AgentToolSucceeded AgentToolStatus = "succeeded"
	AgentToolRejected  AgentToolStatus = "rejected"
	AgentToolFailed    AgentToolStatus = "failed"
)

type AgentToolExecution struct {
	Step           int             `json:"step"`
	CallID         string          `json:"call_id"`
	Name           string          `json:"name"`
	Arguments      string          `json:"arguments"`
	Status         AgentToolStatus `json:"status"`
	DurationMillis int64           `json:"duration_ms"`
	ResultSummary  string          `json:"result_summary,omitempty"`
}

// CandidateFinding is an evidence-bearing hypothesis produced by the model.
// It becomes a Finding only after the verification stage accepts it.
type CandidateFinding struct {
	ID           string   `json:"id"`
	Fingerprint  string   `json:"fingerprint"`
	Title        string   `json:"title"`
	Description  string   `json:"description"`
	Severity     Severity `json:"severity"`
	Category     Category `json:"category"`
	Location     Location `json:"location"`
	Evidence     string   `json:"evidence"`
	Suggestion   string   `json:"suggestion"`
	Confidence   float64  `json:"confidence"`
	Verification []string `json:"verification"`
}

func EmptyAgentRun(status AgentStatus) AgentRun {
	return AgentRun{
		Status:     status,
		Candidates: []CandidateFinding{},
		ToolCalls:  []AgentToolExecution{},
		Warnings:   []string{},
	}
}
