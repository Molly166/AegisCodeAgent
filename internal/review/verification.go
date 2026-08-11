package review

type VerificationStatus string

const (
	VerificationNotRun   VerificationStatus = "not_run"
	VerificationComplete VerificationStatus = "complete"
	VerificationPartial  VerificationStatus = "partial"
	VerificationFailed   VerificationStatus = "failed"
)

type CandidateVerdict string

const (
	CandidateVerified     CandidateVerdict = "verified"
	CandidateRejected     CandidateVerdict = "rejected"
	CandidateInconclusive CandidateVerdict = "inconclusive"
)

type VerificationCheckStatus string

const (
	VerificationCheckPassed  VerificationCheckStatus = "passed"
	VerificationCheckFailed  VerificationCheckStatus = "failed"
	VerificationCheckWarning VerificationCheckStatus = "warning"
)

type VerificationCheck struct {
	Name   string                  `json:"name"`
	Status VerificationCheckStatus `json:"status"`
	Detail string                  `json:"detail"`
}

type CandidateVerification struct {
	CandidateID          string              `json:"candidate_id"`
	Title                string              `json:"title"`
	Severity             Severity            `json:"severity"`
	Location             Location            `json:"location"`
	Verdict              CandidateVerdict    `json:"verdict"`
	Reason               string              `json:"reason"`
	CalibratedConfidence float64             `json:"calibrated_confidence"`
	SourceSnapshot       string              `json:"source_snapshot,omitempty"`
	Checks               []VerificationCheck `json:"checks"`
	MatchedFindingIDs    []string            `json:"matched_finding_ids"`
	FindingID            string              `json:"finding_id,omitempty"`
	Promoted             bool                `json:"promoted"`
}

type VerificationSummary struct {
	Candidates   int `json:"candidates"`
	Verified     int `json:"verified"`
	Rejected     int `json:"rejected"`
	Inconclusive int `json:"inconclusive"`
	Promoted     int `json:"promoted"`
}

type VerificationRun struct {
	Status         VerificationStatus      `json:"status"`
	DurationMillis int64                   `json:"duration_ms"`
	Summary        VerificationSummary     `json:"summary"`
	Tools          []ToolExecution         `json:"tools"`
	Candidates     []CandidateVerification `json:"candidates"`
	Warnings       []string                `json:"warnings"`
}

func EmptyVerificationRun(status VerificationStatus) VerificationRun {
	return VerificationRun{
		Status:     status,
		Tools:      []ToolExecution{},
		Candidates: []CandidateVerification{},
		Warnings:   []string{},
	}
}
