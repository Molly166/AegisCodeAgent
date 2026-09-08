package main

import (
	"strings"
	"testing"

	"github.com/Molly166/AegisCodeAgent/internal/review"
)

func TestPublisherRejectsStaleEvidence(t *testing.T) {
	base, head := strings.Repeat("a", 40), strings.Repeat("b", 40)
	r := review.NewReport(review.Comparison{BaseCommit: base, HeadCommit: head}, nil, nil)
	if err := validateReportIdentity(r, base, head); err != nil {
		t.Fatal(err)
	}
	for _, pair := range [][2]string{{base, strings.Repeat("c", 40)}, {"master", head}, {base, "HEAD"}} {
		if validateReportIdentity(r, pair[0], pair[1]) == nil {
			t.Fatal("accepted stale or symbolic commit identity", pair)
		}
	}
}
