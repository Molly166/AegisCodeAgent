package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Molly166/AegisCodeAgent/internal/review"
)

func TestGitHubRejectsInvalidRiskDomainBeforePublishing(t *testing.T) {
	for _, mutation := range []struct {
		name   string
		change func(*review.ReviewReport)
	}{
		{"verification status", func(r *review.ReviewReport) { r.Verification.Status = "untrusted-private-payload" }},
		{"missing agent status", func(r *review.ReviewReport) { r.Agent.Status = "" }},
		{"severity", func(r *review.ReviewReport) { r.Findings = []review.Finding{{Severity: "untrusted-private-payload"}} }},
		{"hidden P0", func(r *review.ReviewReport) {
			r.Agent.Status = review.AgentComplete
			r.Agent.Candidates = []review.CandidateFinding{{ID: "hypothesis", Severity: review.SeverityCritical}}
			r.Verification.Status = review.VerificationComplete
			r.Verification.Candidates = []review.CandidateVerification{{CandidateID: "hypothesis", Severity: review.SeverityCritical, Verdict: "untrusted-private-payload"}}
		}},
	} {
		t.Run(mutation.name, func(t *testing.T) {
			directory := t.TempDir()
			r := review.NewReport(review.Comparison{}, nil, nil)
			mutation.change(&r)
			encoded, err := json.Marshal(r)
			if err != nil {
				t.Fatal(err)
			}
			reportPath := filepath.Join(directory, "review.json")
			if err := os.WriteFile(reportPath, encoded, 0600); err != nil {
				t.Fatal(err)
			}
			summaryPath := filepath.Join(directory, "summary.md")
			htmlPath := filepath.Join(directory, "review.html")
			var stdout, stderr bytes.Buffer
			code := run(context.Background(), []string{"github", "--report", reportPath, "--summary", summaryPath, "--html-output", htmlPath, "--fail-on-incomplete=false"}, &stdout, &stderr)
			if code == 0 || !strings.Contains(stderr.String(), "unknown or missing") || strings.Contains(stderr.String(), "untrusted-private-payload") {
				t.Fatalf("malformed evidence accepted or leaked: code=%d stderr=%s", code, stderr.String())
			}
			for _, path := range []string{summaryPath, htmlPath} {
				if _, err := os.Stat(path); !os.IsNotExist(err) {
					t.Fatalf("invalid evidence published artifact %s", path)
				}
			}
		})
	}
}
