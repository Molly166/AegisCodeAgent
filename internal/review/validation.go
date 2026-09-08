package review

import (
	"errors"
	"fmt"
)

// ValidateReportDomain validates fields that determine the published risk gate.
// JSON decoding alone accepts arbitrary strings for typed enums; unknown values
// must never collapse to P3, disappear from Needs Review, or appear complete.
// Call after UpgradeReport so known historical verdicts migrate first.
func ValidateReportDomain(report ReviewReport) error {
	switch report.Analysis.Status {
	case AnalysisScopeOnly, AnalysisComplete, AnalysisPartial, AnalysisFailed:
	default:
		return errors.New("analysis has an unknown or missing status")
	}
	switch report.Agent.Status {
	case AgentNotRun, AgentSkipped, AgentComplete, AgentPartial, AgentFailed:
	default:
		return errors.New("agent has an unknown or missing status")
	}
	switch report.Context.Status {
	case ContextNotRun, ContextComplete, ContextPartial, ContextFailed:
	default:
		return errors.New("context has an unknown or missing status")
	}
	switch report.Verification.Status {
	case VerificationNotRun, VerificationComplete, VerificationPartial, VerificationFailed:
	default:
		return errors.New("verification has an unknown or missing status")
	}
	for index, finding := range report.Findings {
		if !knownSeverity(finding.Severity) {
			return fmt.Errorf("finding %d has an unknown or missing severity", index+1)
		}
	}
	for index, candidate := range report.Agent.Candidates {
		if !knownSeverity(candidate.Severity) {
			return fmt.Errorf("agent candidate %d has an unknown or missing severity", index+1)
		}
	}
	for index, candidate := range report.Verification.Candidates {
		if !knownSeverity(candidate.Severity) {
			return fmt.Errorf("verification candidate %d has an unknown or missing severity", index+1)
		}
		switch candidate.Verdict {
		case CandidateVerified, CandidateRejected, CandidateNeedsReview, CandidateInconclusive:
		default:
			return fmt.Errorf("verification candidate %d has an unknown or missing verdict", index+1)
		}
	}
	return nil
}

func knownSeverity(severity Severity) bool {
	switch severity {
	case SeverityCritical, SeverityHigh, SeverityMedium, SeverityLow, SeverityInfo:
		return true
	default:
		return false
	}
}
