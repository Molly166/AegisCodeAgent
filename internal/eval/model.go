package eval

import (
	"time"

	"github.com/Molly166/AegisCodeAgent/internal/githubreport"
	"github.com/Molly166/AegisCodeAgent/internal/review"
)

const (
	CaseSchemaVersion   = "v2"
	ReportSchemaVersion = "v2"
)

type EvaluationMode string

const ModeGoldenReplay EvaluationMode = "golden_replay"

type CaseKind string

const (
	CaseKindBug         CaseKind = "bug"
	CaseKindClean       CaseKind = "clean"
	CaseKindNeedsReview CaseKind = "needs_review"
	CaseKindResilience  CaseKind = "resilience"
)

type EvaluationLayer string

const (
	LayerStatic   EvaluationLayer = "static"
	LayerSemantic EvaluationLayer = "semantic"
	LayerAgent    EvaluationLayer = "agent"
	LayerGate     EvaluationLayer = "gate"
	LayerContext  EvaluationLayer = "context"
	LayerMixed    EvaluationLayer = "mixed"
)

type CaseProvenance string

const (
	ProvenanceRegression       CaseProvenance = "regression"
	ProvenanceCuratedSynthetic CaseProvenance = "curated_synthetic"
)

type ExpectedGate string

const (
	GatePassed  ExpectedGate = "passed"
	GateBlocked ExpectedGate = "blocked"
)

type ExpectedFinding struct {
	Severity      review.Severity `json:"severity"`
	Category      review.Category `json:"category,omitempty"`
	Path          string          `json:"path"`
	StartLine     int             `json:"start_line,omitempty"`
	RuleID        string          `json:"rule_id,omitempty"`
	TitleContains string          `json:"title_contains,omitempty"`
}

type CaseSpec struct {
	SchemaVersion         string            `json:"schema_version"`
	ID                    string            `json:"id"`
	Title                 string            `json:"title"`
	Description           string            `json:"description,omitempty"`
	Kind                  CaseKind          `json:"kind"`
	Layer                 EvaluationLayer   `json:"layer"`
	Provenance            CaseProvenance    `json:"provenance"`
	Tags                  []string          `json:"tags"`
	Report                string            `json:"report"`
	ExpectedFindings      []ExpectedFinding `json:"expected_findings"`
	ExpectedGate          ExpectedGate      `json:"expected_gate"`
	ExpectedNeedsReview   int               `json:"expected_needs_review"`
	MaxUnexpectedFindings int               `json:"max_unexpected_findings"`
}

type FindingMatch struct {
	Expected ExpectedFinding `json:"expected"`
	Actual   review.Finding  `json:"actual"`
}

type CaseResult struct {
	ID                    string            `json:"id"`
	Title                 string            `json:"title"`
	Description           string            `json:"description,omitempty"`
	Kind                  CaseKind          `json:"kind"`
	Layer                 EvaluationLayer   `json:"layer"`
	Provenance            CaseProvenance    `json:"provenance"`
	Tags                  []string          `json:"tags"`
	Passed                bool              `json:"passed"`
	ExpectedGate          ExpectedGate      `json:"expected_gate"`
	ActualGate            ExpectedGate      `json:"actual_gate"`
	GateCorrect           bool              `json:"gate_correct"`
	Matches               []FindingMatch    `json:"matches"`
	Missed                []ExpectedFinding `json:"missed"`
	Unexpected            []review.Finding  `json:"unexpected"`
	NeedsReview           int               `json:"needs_review"`
	ExpectedNeedsReview   int               `json:"expected_needs_review"`
	MaxUnexpectedFindings int               `json:"max_unexpected_findings"`
	ReportPath            string            `json:"report_path"`
	AgentTokens           int               `json:"agent_tokens"`
	AgentDurationMillis   int64             `json:"agent_duration_ms"`
}

type Metrics struct {
	Cases                int     `json:"cases"`
	PassedCases          int     `json:"passed_cases"`
	ExpectedFindings     int     `json:"expected_findings"`
	ActualFindings       int     `json:"actual_findings"`
	MatchedFindings      int     `json:"matched_findings"`
	FalsePositives       int     `json:"false_positives"`
	FalseNegatives       int     `json:"false_negatives"`
	Precision            float64 `json:"precision"`
	Recall               float64 `json:"recall"`
	F1                   float64 `json:"f1"`
	P0Expected           int     `json:"p0_expected"`
	P0Matched            int     `json:"p0_matched"`
	P0Recall             float64 `json:"p0_recall"`
	P1Expected           int     `json:"p1_expected"`
	P1Matched            int     `json:"p1_matched"`
	P1Recall             float64 `json:"p1_recall"`
	P2Expected           int     `json:"p2_expected"`
	P2Matched            int     `json:"p2_matched"`
	P2Recall             float64 `json:"p2_recall"`
	P3Expected           int     `json:"p3_expected"`
	P3Matched            int     `json:"p3_matched"`
	P3Recall             float64 `json:"p3_recall"`
	GateCorrect          int     `json:"gate_correct"`
	GateAccuracy         float64 `json:"gate_accuracy"`
	BugCases             int     `json:"bug_cases"`
	CleanCases           int     `json:"clean_cases"`
	NeedsReviewCases     int     `json:"needs_review_cases"`
	ResilienceCases      int     `json:"resilience_cases"`
	FalseBlocks          int     `json:"false_blocks"`
	FalseBlockRate       float64 `json:"false_block_rate"`
	UnresolvedHypotheses int     `json:"unresolved_hypotheses"`
	AgentTokens          int     `json:"agent_tokens"`
	AgentDurationMillis  int64   `json:"agent_duration_ms"`
}

type HarnessReport struct {
	SchemaVersion string               `json:"schema_version"`
	Mode          EvaluationMode       `json:"mode"`
	GeneratedAt   time.Time            `json:"generated_at"`
	Corpus        string               `json:"corpus"`
	Gate          githubreport.Options `json:"gate"`
	Metrics       Metrics              `json:"metrics"`
	Cases         []CaseResult         `json:"cases"`
}
