package review

import (
	"strings"
	"testing"
)

func TestValidateReportDomainRejectsMissingOrUnknownGateFields(t *testing.T) {
	mutations := []struct {
		name   string
		change func(*ReviewReport, string)
	}{
		{"analysis", func(r *ReviewReport, v string) { r.Analysis.Status = AnalysisStatus(v) }},
		{"agent", func(r *ReviewReport, v string) { r.Agent.Status = AgentStatus(v) }},
		{"context", func(r *ReviewReport, v string) { r.Context.Status = ContextStatus(v) }},
		{"verification", func(r *ReviewReport, v string) { r.Verification.Status = VerificationStatus(v) }},
		{"finding severity", func(r *ReviewReport, v string) { r.Findings = []Finding{{Severity: Severity(v)}} }},
		{"agent severity", func(r *ReviewReport, v string) { r.Agent.Candidates = []CandidateFinding{{Severity: Severity(v)}} }},
		{"verified severity", func(r *ReviewReport, v string) {
			r.Verification.Candidates = []CandidateVerification{{Severity: Severity(v), Verdict: CandidateNeedsReview}}
		}},
		{"verdict", func(r *ReviewReport, v string) {
			r.Verification.Candidates = []CandidateVerification{{Severity: SeverityCritical, Verdict: CandidateVerdict(v)}}
		}},
	}
	for _, mutation := range mutations {
		for _, value := range []string{"", "untrusted-private-payload"} {
			t.Run(mutation.name+"/"+value, func(t *testing.T) {
				r := NewReport(Comparison{}, nil, nil)
				mutation.change(&r, value)
				err := ValidateReportDomain(r)
				if err == nil || strings.Contains(err.Error(), "untrusted-private-payload") {
					t.Fatalf("invalid domain value accepted or echoed: %v", err)
				}
			})
		}
	}
}

func TestValidateReportDomainAcceptsKnownStatesWithoutAssumingComplete(t *testing.T) {
	r := NewReport(Comparison{}, nil, nil)
	for _, status := range []AnalysisStatus{AnalysisScopeOnly, AnalysisComplete, AnalysisPartial, AnalysisFailed} {
		r.Analysis.Status = status
		if err := ValidateReportDomain(r); err != nil {
			t.Fatal(err)
		}
	}
	for _, status := range []AgentStatus{AgentNotRun, AgentSkipped, AgentComplete, AgentPartial, AgentFailed} {
		r.Agent.Status = status
		if err := ValidateReportDomain(r); err != nil {
			t.Fatal(err)
		}
	}
	for _, status := range []ContextStatus{ContextNotRun, ContextComplete, ContextPartial, ContextFailed} {
		r.Context.Status = status
		if err := ValidateReportDomain(r); err != nil {
			t.Fatal(err)
		}
	}
	for _, status := range []VerificationStatus{VerificationNotRun, VerificationComplete, VerificationPartial, VerificationFailed} {
		r.Verification.Status = status
		if err := ValidateReportDomain(r); err != nil {
			t.Fatal(err)
		}
	}
	for _, severity := range []Severity{SeverityCritical, SeverityHigh, SeverityMedium, SeverityLow, SeverityInfo} {
		r.Findings = []Finding{{Severity: severity}}
		r.Agent.Candidates = []CandidateFinding{{Severity: severity}}
		for _, verdict := range []CandidateVerdict{CandidateVerified, CandidateRejected, CandidateNeedsReview, CandidateInconclusive} {
			r.Verification.Candidates = []CandidateVerification{{Severity: severity, Verdict: verdict}}
			if err := ValidateReportDomain(r); err != nil {
				t.Fatal(err)
			}
		}
	}
}

func TestDomainValidationPreservesV5Migration(t *testing.T) {
	r := NewReport(Comparison{}, nil, nil)
	r.SchemaVersion = PreviousSchemaVersion
	r.Verification.Candidates = []CandidateVerification{{Severity: SeverityCritical, Verdict: CandidateInconclusive}}
	if err := UpgradeReport(&r); err != nil {
		t.Fatal(err)
	}
	if err := ValidateReportDomain(r); err != nil {
		t.Fatal(err)
	}
	if r.Verification.Candidates[0].Verdict != CandidateNeedsReview {
		t.Fatal("legacy verdict was not migrated before validation")
	}
	minimal := ReviewReport{SchemaVersion: PreviousSchemaVersion}
	if err := UpgradeReport(&minimal); err != nil {
		t.Fatalf("additive migration changed its contract: %v", err)
	}
	if ValidateReportDomain(minimal) == nil {
		t.Fatal("missing historical status was silently treated as complete")
	}
}
