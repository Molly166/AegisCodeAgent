package liveeval

import (
	"math"
	"sort"
	"strings"

	"github.com/Molly166/AegisCodeAgent/internal/githubreport"
	"github.com/Molly166/AegisCodeAgent/internal/review"
)

func match(expected []ExpectedFinding, actual []review.Finding) (matched, missed []string, unexpected []review.Finding) {
	matched = []string{}
	missed = []string{}
	unexpected = []review.Finding{}
	// Maximum one-to-one matching avoids order-dependent greedy matches when
	// two independent expectation rubrics overlap.
	assigned := make([]int, len(actual))
	for i := range assigned {
		assigned[i] = -1
	}
	var assign func(int, []bool) bool
	assign = func(e int, visited []bool) bool {
		for i, f := range actual {
			if visited[i] || !matches(expected[e], f) {
				continue
			}
			visited[i] = true
			if assigned[i] < 0 || assign(assigned[i], visited) {
				assigned[i] = e
				return true
			}
		}
		return false
	}
	for i := range expected {
		assign(i, make([]bool, len(actual)))
	}
	found := make([]bool, len(expected))
	for i, e := range assigned {
		if e >= 0 {
			found[e] = true
		} else {
			unexpected = append(unexpected, actual[i])
		}
	}
	for i, e := range expected {
		if found[i] {
			matched = append(matched, e.ID)
		} else {
			missed = append(missed, e.ID)
		}
	}
	return matched, missed, unexpected
}

func matches(expected ExpectedFinding, actual review.Finding) bool {
	if githubreport.PriorityForSeverity(actual.Severity) != expected.Priority || actual.Category != expected.Category || actual.Location.Path != expected.Path || actual.Location.StartLine < expected.StartLine || actual.Location.StartLine > expected.EndLine {
		return false
	}
	text := strings.ToLower(actual.Title + "\n" + actual.Description + "\n" + actual.Evidence)
	for _, term := range expected.EvidenceTermsAny {
		if strings.Contains(text, strings.ToLower(term)) {
			return true
		}
	}
	return false
}

func ratio(numerator, denominator int) Rate {
	r := Rate{Numerator: numerator, Denominator: denominator}
	if denominator > 0 {
		value := float64(numerator) / float64(denominator)
		r.Value = &value
	}
	return r
}

func calculateMetrics(runs []RunResult, liveModel bool) Metrics {
	m := Metrics{Runs: len(runs), PriorityRecall: map[string]Rate{}}
	var matched, expected, actual, gates, clean, falseBlocks, incomplete, completed int
	priorityExpected := map[string]int{}
	priorityMatched := map[string]int{}
	groups := map[string][]RunResult{}
	durations := []int64{}
	for _, r := range runs {
		groups[r.CaseID] = append(groups[r.CaseID], r)
		if r.Passed {
			m.PassedRuns++
		}
		matched += len(r.MatchedIDs)
		expected += len(r.Expected)
		actual += len(r.MatchedIDs) + len(r.Unexpected)
		if !r.Incomplete && r.ActualGate == r.ExpectedGate {
			gates++
		}
		if r.Kind == "clean" && r.ActualGate != "unknown" {
			clean++
			if r.ActualGate == "blocked" {
				falseBlocks++
			}
		}
		if r.Incomplete {
			incomplete++
		}
		if r.AgentCompleted {
			completed++
		}
		m.Tokens += r.Tokens
		m.MeanDurationMillis += float64(r.DurationMillis)
		durations = append(durations, r.DurationMillis)
		ids := map[string]bool{}
		for _, id := range r.MatchedIDs {
			ids[id] = true
		}
		for _, e := range r.Expected {
			key := strings.ToUpper(string(e.Priority))
			priorityExpected[key]++
			if ids[e.ID] {
				priorityMatched[key]++
			}
		}
	}
	m.UniqueCases = len(groups)
	m.Precision = ratio(matched, actual)
	m.Recall = ratio(matched, expected)
	m.GateAccuracy = ratio(gates, len(runs))
	m.FalseBlockRate = ratio(falseBlocks, clean)
	m.IncompleteRate = ratio(incomplete, len(runs))
	if liveModel {
		m.AgentCompletionRate = ratio(completed, len(runs))
	}
	var consistent, repeated int
	for _, group := range groups {
		if len(group) < 2 {
			continue
		}
		repeated++
		allSame := true
		for _, run := range group {
			if run.Incomplete || run.ActualGate != group[0].ActualGate {
				allSame = false
			}
		}
		if allSame {
			consistent++
		}
	}
	m.GateConsistency = ratio(consistent, repeated)
	for _, p := range []string{"P0", "P1", "P2", "P3"} {
		m.PriorityRecall[p] = ratio(priorityMatched[p], priorityExpected[p])
	}
	if len(runs) > 0 {
		m.MeanDurationMillis /= float64(len(runs))
		sort.Slice(durations, func(i, j int) bool { return durations[i] < durations[j] })
		m.P95DurationMillis = durations[int(math.Ceil(float64(len(durations))*.95))-1]
	}
	return m
}
