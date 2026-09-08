package main

import (
	"fmt"
	"regexp"

	"github.com/Molly166/AegisCodeAgent/internal/review"
)

var fullCommitPattern = regexp.MustCompile(`^[0-9a-f]{40}([0-9a-f]{24})?$`)

func validateReportIdentity(report review.ReviewReport, base, head string) error {
	for _, item := range []struct{ name, expected, actual string }{
		{"base", base, report.Comparison.BaseCommit}, {"head", head, report.Comparison.HeadCommit},
	} {
		if item.expected == "" {
			continue
		}
		if !fullCommitPattern.MatchString(item.expected) {
			return fmt.Errorf("expected %s must be a full commit SHA", item.name)
		}
		if item.actual != item.expected {
			return fmt.Errorf("report %s commit does not match trusted workflow input", item.name)
		}
	}
	return nil
}
