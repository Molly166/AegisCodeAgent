package githubreport

import (
	"encoding/json"

	"github.com/Molly166/AegisCodeAgent/internal/review"
)

// RenderGateJSON lets delivery consumers use the same policy decision as the
// HTML and PR summary, without parsing prose or duplicating severity rules.
func RenderGateJSON(report review.ReviewReport, options Options) ([]byte, error) {
	gate := EvaluateWithOptions(report, options)
	return json.MarshalIndent(struct {
		SchemaVersion int    `json:"schema_version"`
		Base          string `json:"base"`
		Head          string `json:"head"`
		Blocked       bool   `json:"blocked"`
		Incomplete    bool   `json:"incomplete"`
		Degraded      bool   `json:"degraded"`
		NeedsReview   bool   `json:"needs_review"`
	}{1, report.Comparison.BaseCommit, report.Comparison.HeadCommit,
		gate.Blocked, gate.Incomplete, gate.Degraded, gate.NeedsReview}, "", "  ")
}
