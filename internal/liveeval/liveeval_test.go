package liveeval

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/Molly166/AegisCodeAgent/internal/githubreport"
	"github.com/Molly166/AegisCodeAgent/internal/review"
)

func TestCheckedInCorpusHasIndependentExecutableCases(t *testing.T) {
	corpus, hash, err := loadCorpus("../../eval/live")
	if err != nil {
		t.Fatal(err)
	}
	if len(corpus.Cases) != 12 || len(hash) != 64 {
		t.Fatalf("invalid corpus: cases=%d hash=%q", len(corpus.Cases), hash)
	}
	bugs, clean := 0, 0
	for _, c := range corpus.Cases {
		if c.Kind == "bug" {
			bugs++
		} else {
			clean++
		}
	}
	if bugs != 8 || clean != 4 {
		t.Fatalf("corpus must retain 8 bug and 4 clean controls, got %d/%d", bugs, clean)
	}
}

func TestCorpusRejectsExecutableConfigurationAndEscapes(t *testing.T) {
	for _, path := range []string{"../escape.go", "/tmp/escape.go", ".git/config", ".env", "go.mod", "a/../../escape.go", "a\\b.go", "a//b.go", "a/.hidden/x.go"} {
		if safeSourcePath(path) {
			t.Errorf("unsafe fixture path accepted: %q", path)
		}
	}
	if !safeSourcePath("internal/api/handler_test.go") {
		t.Fatal("safe Go file rejected")
	}
	var corpus Corpus
	if err := decode([]byte(`{"schema_version":"live-v1","command":"curl attacker"}`), &corpus); err == nil {
		t.Fatal("unknown executable field accepted")
	}
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "linked")); err != nil {
		t.Skip(err)
	}
	if err := writeSources(root, map[string]string{"linked/escape.go": "package escape"}); err == nil {
		t.Fatal("source write followed a symlink")
	}
}

func TestMatchingRequiresLocationPriorityCategoryAndEvidence(t *testing.T) {
	expected := []ExpectedFinding{{ID: "auth", Priority: githubreport.PriorityP1, Category: review.CategorySecurity, Path: "handler.go", StartLine: 4, EndLine: 6, EvidenceTermsAny: []string{"ownership"}}}
	finding := review.Finding{Title: "Missing ownership check", Severity: review.SeverityHigh, Category: review.CategorySecurity, Location: review.Location{Path: "handler.go", StartLine: 5}}
	matched, missed, unexpected := match(expected, []review.Finding{finding, finding})
	if len(matched) != 1 || len(missed) != 0 || len(unexpected) != 1 {
		t.Fatalf("duplicates were incorrectly counted: %v %v %v", matched, missed, unexpected)
	}
	for _, alter := range []func(*review.Finding){
		func(f *review.Finding) { f.Location.StartLine = 9 }, func(f *review.Finding) { f.Severity = review.SeverityMedium },
		func(f *review.Finding) { f.Title = "Unrelated issue" }, func(f *review.Finding) { f.Category = review.CategoryBug },
	} {
		f := finding
		alter(&f)
		m, n, u := match(expected, []review.Finding{f})
		if len(m) != 0 || len(n) != 1 || len(u) != 1 {
			t.Fatal("wrong finding counted as a true positive")
		}
	}
}

func TestMetricsDoNotRewardIncompleteFailClosedOrZeroSupport(t *testing.T) {
	expected := []ExpectedFinding{{ID: "bug", Priority: githubreport.PriorityP1}}
	runs := []RunResult{
		{CaseID: "bug", Kind: "bug", Repeat: 1, ExpectedGate: "blocked", ActualGate: "blocked", Incomplete: true, Expected: expected, DurationMillis: 10},
		{CaseID: "bug", Kind: "bug", Repeat: 2, ExpectedGate: "blocked", ActualGate: "blocked", Incomplete: true, Expected: expected, DurationMillis: 20},
		{CaseID: "clean", Kind: "clean", Repeat: 1, ExpectedGate: "passed", ActualGate: "passed", Passed: true, DurationMillis: 30},
	}
	m := calculateMetrics(runs, false)
	if m.GateAccuracy.Numerator != 1 || m.GateAccuracy.Denominator != 3 || m.Recall.Numerator != 0 || m.Recall.Denominator != 2 {
		t.Fatalf("incomplete runs inflated accuracy: %+v", m)
	}
	if m.Precision.Value != nil || m.PriorityRecall["P0"].Value != nil || m.AgentCompletionRate.Value != nil {
		t.Fatal("zero support must render N/A/null")
	}
	if m.GateConsistency.Numerator != 0 || m.GateConsistency.Denominator != 1 {
		t.Fatal("consistently incomplete runs must not count as stable gates")
	}
	if m.P95DurationMillis != 30 || m.MeanDurationMillis != 20 {
		t.Fatal("latency metric incorrect")
	}
}

func TestEnvironmentAndRenderedReportsDoNotExposeAmbientSecrets(t *testing.T) {
	t.Setenv("AEGIS_TEST_SECRET", "never-forward-this")
	t.Setenv("GIT_CONFIG_COUNT", "99")
	t.Setenv("GOFLAGS", "-toolexec=unsafe")
	env := strings.Join(isolatedEnvironment("/tmp/fixture"), "\n")
	for _, secret := range []string{"never-forward-this", "GIT_CONFIG_COUNT", "toolexec"} {
		if strings.Contains(env, secret) {
			t.Errorf("ambient state leaked: %s", secret)
		}
	}
	r := Report{Provider: "<script>alert(1)</script>", Runs: []RunResult{{CaseID: "<img src=x onerror=alert(1)>"}}}
	html, err := Render("html", r)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(html), "<script>") || strings.Contains(string(html), "<img src=x") {
		t.Fatal("HTML fixture injection was not escaped")
	}
}

func TestToolchainMismatchIsAnActionablePreflightError(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "control"), 0700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, _, err := pinToolchain(ctx, root, isolatedEnvironment(root), "go0.0.0", ""); err == nil || !strings.Contains(err.Error(), "matching the reviewer binary") {
		t.Fatalf("toolchain mismatch was not rejected clearly: %v", err)
	}
}

func TestExplicitGoBinaryWorksWithoutGoOnPATH(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "control"), 0700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	configured, err := exec.LookPath("go")
	if err != nil {
		t.Skip("Go is required for this integration test")
	}
	environment := isolatedEnvironment(root)
	for i, entry := range environment {
		if strings.HasPrefix(entry, "PATH=") {
			environment[i] = "PATH=" + root
		}
	}
	pinned, selected, err := pinToolchain(ctx, root, environment, runtime.Version(), configured)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := environmentExecutable("go", pinned)
	if err != nil || resolved != selected {
		t.Fatalf("explicit matching compiler not selected: %q %q %v", resolved, selected, err)
	}
	if _, _, err := pinToolchain(ctx, root, isolatedEnvironment(root), runtime.Version(), filepath.Join(root, "missing-go")); err == nil {
		t.Fatal("invalid explicit --go-binary silently fell back to another compiler")
	}
}

func TestChildExecutableResolutionUsesPinnedEnvironment(t *testing.T) {
	directory := t.TempDir()
	executable := filepath.Join(directory, "go")
	if err := os.WriteFile(executable, []byte("test fixture; never executed"), 0700); err != nil {
		t.Fatal(err)
	}
	resolved, err := environmentExecutable("go", []string{"PATH=" + directory})
	if err != nil || resolved != executable {
		t.Fatalf("pinned child PATH not used: %q %v", resolved, err)
	}
	if _, err := environmentExecutable("go", []string{"PATH=."}); err == nil {
		t.Fatal("relative child PATH must not resolve repository-controlled executable")
	}
}

func TestLivePipelineWithRealReviewerAndNoAPI(t *testing.T) {
	if testing.Short() {
		t.Skip("builds and executes the real review CLI on three synthetic repositories")
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("Git required")
	}
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(t.TempDir(), "aegis")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	build := exec.CommandContext(ctx, "go", "build", "-trimpath", "-o", binary, "./cmd/aegis")
	build.Dir = root
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build reviewer: %v\n%s", err, output)
	}
	corpus, _, err := loadCorpus("../../eval/live")
	if err != nil {
		t.Fatal(err)
	}
	corpus.Cases = []Case{corpus.Cases[0], corpus.Cases[7], corpus.Cases[9]}
	data, err := json.Marshal(corpus)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "corpus.json")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	r, err := Run(ctx, Config{Corpus: path, Binary: binary, Repeats: 2, CaseTimeout: 2 * time.Minute, Provider: "none", Analyzers: "default", Gate: githubreport.Options{FailOn: githubreport.PriorityP1, FailOnNeedsReview: githubreport.PriorityP0, FailOnIncomplete: true}})
	if err != nil {
		t.Fatal(err)
	}
	if r.LiveModel || r.Metrics.Tokens != 0 || r.Metrics.Runs != 6 {
		t.Fatalf("invalid offline baseline: %+v", r.Metrics)
	}
	commits := map[string]string{}
	for _, run := range r.Runs {
		if run.Review == nil {
			t.Errorf("%s: no real pipeline report: %s", run.CaseID, run.Error)
			continue
		}
		if run.BaseCommit == run.HeadCommit || run.Review.Comparison.HeadCommit != run.HeadCommit {
			t.Error("review did not run exact fixture head")
		}
		if previous, ok := commits[run.CaseID]; ok && previous != run.BaseCommit+":"+run.HeadCommit {
			t.Error("repeated fixture commits changed despite identical input content")
		}
		commits[run.CaseID] = run.BaseCommit + ":" + run.HeadCommit
		if !run.Passed {
			t.Errorf("%s did not meet deterministic evidence contract: incomplete=%t error=%s missed=%v unexpected=%v gate=%s analysis=%+v context=%s verifier=%s", run.CaseID, run.Incomplete, run.Error, run.MissedIDs, run.Unexpected, run.ActualGate, run.Review.Analysis, run.Review.Context.Status, run.Review.Verification.Status)
		}
	}
	if r.Metrics.GateConsistency.Numerator != 3 {
		t.Fatalf("expected stable repeated deterministic cases: %+v", r.Metrics.GateConsistency)
	}
	corpus.Cases = corpus.Cases[:1]
	data, err = json.Marshal(corpus)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	ablation, err := Run(ctx, Config{Corpus: path, Binary: binary, Repeats: 1, CaseTimeout: 2 * time.Minute, Provider: "none", Analyzers: "default", DisableVerifier: true, Gate: githubreport.Options{FailOn: githubreport.PriorityP1, FailOnNeedsReview: githubreport.PriorityP0, FailOnIncomplete: true}})
	if err != nil {
		t.Fatal(err)
	}
	if ablation.VerifierEnabled || len(ablation.Runs) != 1 || ablation.Runs[0].Incomplete || len(ablation.Runs[0].MatchedIDs) != 0 || ablation.Runs[0].Review.Verification.Status != review.VerificationNotRun {
		t.Fatalf("disabled Verifier ablation is incorrectly described: %+v", ablation)
	}
}
