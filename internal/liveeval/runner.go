package liveeval

import (
	"bytes"
	"context"
	"crypto/sha256"
	"debug/buildinfo"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/Molly166/AegisCodeAgent/internal/analyzer"
	"github.com/Molly166/AegisCodeAgent/internal/githubreport"
	"github.com/Molly166/AegisCodeAgent/internal/review"
)

func Run(ctx context.Context, cfg Config) (Report, error) {
	if cfg.Repeats < 1 || cfg.Repeats > 10 || cfg.CaseTimeout <= 0 || cfg.CaseTimeout > time.Hour {
		return Report{}, errors.New("repeats must be 1..10 and case-timeout must be positive and at most 1h")
	}
	if cfg.Provider == "" {
		cfg.Provider = "none"
	}
	switch cfg.Provider {
	case "none", "deepseek", "orcarouter", "openai-compatible":
	default:
		return Report{}, errors.New("unsupported live eval provider")
	}
	if cfg.Provider != "none" && strings.TrimSpace(cfg.Model) == "" {
		return Report{}, errors.New("live model runs require an explicit --agent-model")
	}
	if cfg.Analyzers == "" {
		cfg.Analyzers = "default"
	}
	if _, err := analyzer.ParseSelection(cfg.Analyzers); err != nil {
		return Report{}, err
	}
	if cfg.Gate.FailOn == "" {
		cfg.Gate.FailOn = githubreport.PriorityP1
	}
	if cfg.Gate.FailOnNeedsReview == "" {
		cfg.Gate.FailOnNeedsReview = githubreport.PriorityP0
	}
	if cfg.Provider != "none" {
		if cfg.APIKeyEnv == "" {
			switch cfg.Provider {
			case "deepseek":
				cfg.APIKeyEnv = "DEEPSEEK_API_KEY"
			case "orcarouter":
				cfg.APIKeyEnv = "ORCAROUTER_API_KEY"
			default:
				cfg.APIKeyEnv = "AEGIS_API_KEY"
			}
		}
		// Only key-shaped names are allowed: the normal reviewer strips these
		// before any repository-controlled analyzer/test subprocess starts.
		if !caseIDPattern.MatchString(cfg.APIKeyEnv) || !strings.HasSuffix(cfg.APIKeyEnv, "_KEY") {
			return Report{}, errors.New("agent-api-key-env must be a safe identifier ending in _KEY")
		}
		if strings.TrimSpace(os.Getenv(cfg.APIKeyEnv)) == "" {
			return Report{}, errors.New("live model credential is missing from the selected environment variable")
		}
	}
	corpus, corpusHash, err := loadCorpus(cfg.Corpus)
	if err != nil {
		return Report{}, fmt.Errorf("load live corpus: %w", err)
	}
	binary, err := filepath.Abs(cfg.Binary)
	if err != nil || cfg.Binary == "" {
		return Report{}, errors.New("aegis-binary must identify a trusted built executable")
	}
	binary, err = filepath.EvalSymlinks(binary)
	if err != nil {
		return Report{}, fmt.Errorf("resolve aegis binary: %w", err)
	}
	binaryData, err := readRegularFile(binary, 512*1024*1024)
	if err != nil {
		return Report{}, fmt.Errorf("read aegis binary: %w", err)
	}
	binaryDigest := sha256.Sum256(binaryData)
	build, err := buildinfo.Read(bytes.NewReader(binaryData))
	if err != nil {
		return Report{}, errors.New("aegis-binary must be a Go executable with readable build metadata")
	}
	root, err := os.MkdirTemp("", "aegis-live-eval-")
	if err != nil {
		return Report{}, err
	}
	defer os.RemoveAll(root)
	// Snapshot the executable before any case runs, so a concurrent local
	// rebuild cannot silently change the evaluator behind BinarySHA256.
	cfg.Binary = filepath.Join(root, "reviewer-"+filepath.Base(binary))
	// #nosec G306 -- this owner-only snapshot must be executable; it contains
	// the explicitly selected trusted reviewer, not corpus-supplied source.
	if err := os.WriteFile(cfg.Binary, binaryData, 0700); err != nil {
		return Report{}, fmt.Errorf("snapshot trusted reviewer: %w", err)
	}
	for _, name := range []string{"home", "tmp", "go-cache", "go-path", "go-mod", "control"} {
		if err := os.Mkdir(filepath.Join(root, name), 0700); err != nil {
			return Report{}, err
		}
	}
	environment := isolatedEnvironment(root)
	environment, selectedGo, err := pinToolchain(ctx, root, environment, build.GoVersion, cfg.GoBinary)
	if err != nil {
		return Report{}, err
	}
	reviewerEnvironment := append([]string{}, environment...)
	if cfg.Provider != "none" {
		reviewerEnvironment = append(reviewerEnvironment, cfg.APIKeyEnv+"="+os.Getenv(cfg.APIKeyEnv))
	}
	result := Report{
		SchemaVersion: SchemaVersion, Mode: "live_pipeline", GeneratedAt: time.Now().UTC(), CorpusSHA256: corpusHash,
		BinarySHA256: hex.EncodeToString(binaryDigest[:]), GoToolchain: build.GoVersion, GoBinary: selectedGo, Provider: cfg.Provider, RequestedModel: cfg.Model,
		LiveModel: cfg.Provider != "none", Repeats: cfg.Repeats, Analyzers: cfg.Analyzers, VerifierEnabled: !cfg.DisableVerifier, Gate: cfg.Gate,
		Limitations: []string{
			"Curated synthetic local fixtures; results do not establish production repository accuracy.",
			"Finding matching uses independently declared priority/category/location/lexical evidence constraints, not an LLM judge; inspect missed and unexpected findings manually.",
			"Repeated runs share cases and are correlated; support counts are shown and no independent-sample confidence interval is asserted.",
			"No provider price catalog is assumed. Token counts are observed usage; monetary cost is not inferred.",
			"Fixture code executes with a sanitized environment, offline Go dependencies, and time limits; this is not an OS sandbox for untrusted corpora.",
		}, Runs: []RunResult{},
	}
	for _, c := range corpus.Cases {
		for repeat := 1; repeat <= cfg.Repeats; repeat++ {
			runCtx, cancel := context.WithTimeout(ctx, cfg.CaseTimeout)
			r := runCase(runCtx, cfg, c, repeat, root, environment, reviewerEnvironment)
			cancel()
			result.Runs = append(result.Runs, r)
		}
	}
	result.Metrics = calculateMetrics(result.Runs, cfg.Provider != "none")
	return result, nil
}

func runCase(ctx context.Context, cfg Config, c Case, repeat int, root string, environment, reviewerEnvironment []string) (r RunResult) {
	started := time.Now()
	r = RunResult{CaseID: c.ID, Title: c.Title, Kind: c.Kind, Repeat: repeat, ExpectedGate: c.ExpectedGate, ActualGate: "unknown", Expected: c.Expected, MatchedIDs: []string{}, MissedIDs: []string{}, Unexpected: []review.Finding{}}
	for _, e := range c.Expected {
		r.MissedIDs = append(r.MissedIDs, e.ID)
	}
	defer func() { r.DurationMillis = time.Since(started).Milliseconds() }()
	fail := func(message string) RunResult { r.Incomplete = true; r.Error = message; return r }
	if ctx.Err() != nil {
		return fail("evaluation canceled or total time budget exhausted")
	}
	workspace, err := os.MkdirTemp(root, c.ID+"-")
	if err != nil {
		return fail("create isolated case workspace failed")
	}
	repo := filepath.Join(workspace, "repo")
	if err := os.Mkdir(repo, 0700); err != nil {
		return fail("create fixture repository failed")
	}
	if err := writeSources(repo, c.Base); err != nil {
		return fail("materialize fixture base failed")
	}
	if err := os.WriteFile(filepath.Join(repo, "go.mod"), []byte("module example.com/aegisfixture\n\ngo 1.23\n"), 0600); err != nil {
		return fail("materialize fixture module failed")
	}
	git := func(args ...string) (string, error) {
		return execute(ctx, repo, environment, "git", append([]string{"-c", "core.hooksPath=/dev/null", "-c", "commit.gpgsign=false"}, args...)...)
	}
	if _, err := git("init", "--quiet", "--template="); err != nil {
		return fail("initialize local fixture Git repository failed")
	}
	commit := func(message string) (string, error) {
		if _, err := git("add", "--all"); err != nil {
			return "", err
		}
		if _, err := git("-c", "user.name=Aegis Eval", "-c", "user.email=aegis-eval@example.invalid", "commit", "--quiet", "-m", message); err != nil {
			return "", err
		}
		sha, err := git("rev-parse", "HEAD")
		return strings.TrimSpace(sha), err
	}
	r.BaseCommit, err = commit("Fixture base")
	if err != nil {
		return fail("create fixture base commit failed")
	}
	if _, err := execute(ctx, repo, environment, "go", "test", "-count=1", "./..."); err != nil {
		return fail("fixture base go test failed (" + commandFailure(err) + "); labels cannot be evaluated against a broken baseline")
	}
	if err := writeSources(repo, c.Head); err != nil {
		return fail("materialize fixture head failed")
	}
	r.HeadCommit, err = commit("Fixture head")
	if err != nil {
		return fail("create fixture head commit failed")
	}
	reportPath := filepath.Join(workspace, "review.json")
	args := []string{"review", "--repo", repo, "--base", r.BaseCommit, "--head", r.HeadCommit, "--format", "json", "--output", reportPath,
		"--config=", "--github-event=", "--agent-no-dotenv", "--analyzers", cfg.Analyzers, "--agent-provider", cfg.Provider,
		"--analyzer-timeout", cfg.CaseTimeout.String(), "--context-timeout", cfg.CaseTimeout.String(), "--agent-timeout", cfg.CaseTimeout.String(), "--verifier-timeout", cfg.CaseTimeout.String(),
	}
	if cfg.Provider != "none" {
		args = append(args, "--agent-model", cfg.Model, "--agent-api-key-env", cfg.APIKeyEnv)
	}
	if cfg.BaseURL != "" {
		args = append(args, "--agent-base-url", cfg.BaseURL)
	}
	if cfg.AllowCustomEndpoint {
		args = append(args, "--agent-allow-custom-endpoint")
	}
	if cfg.DisableVerifier {
		args = append(args, "--verify-agent-candidates=false")
	}
	_, commandErr := execute(ctx, filepath.Join(root, "control"), reviewerEnvironment, cfg.Binary, args...)
	data, err := readRegularFile(reportPath, 32*1024*1024)
	if err != nil {
		return fail("review process did not produce a bounded regular JSON report (" + commandFailure(commandErr) + ")")
	}
	var report review.ReviewReport
	if err := decode(data, &report); err != nil {
		return fail("review process produced an invalid report schema")
	}
	if err := review.UpgradeReport(&report); err != nil {
		return fail("review report uses an incompatible schema")
	}
	if report.Comparison.BaseCommit != r.BaseCommit || report.Comparison.HeadCommit != r.HeadCommit {
		return fail("review report does not match the exact fixture base/head commits")
	}
	r.Review = &report
	r.Tokens = report.Agent.Usage.TotalTokens
	r.AgentStatus = report.Agent.Status
	r.AgentCompleted = report.Agent.Status == review.AgentComplete
	r.NeedsReview = report.Verification.Summary.NeedsReview + report.Verification.Summary.Inconclusive
	if cfg.DisableVerifier {
		r.NeedsReview += len(report.Agent.Candidates)
	}
	gate := githubreport.EvaluateWithOptions(report, cfg.Gate)
	r.ActualGate = "passed"
	if gate.Blocked {
		r.ActualGate = "blocked"
	}
	r.Incomplete = commandErr != nil || gate.Incomplete || gate.Degraded || report.Analysis.Status != review.AnalysisComplete || report.Context.Status != review.ContextComplete || (!cfg.DisableVerifier && report.Verification.Status != review.VerificationComplete) || (cfg.Provider != "none" && !r.AgentCompleted)
	if commandErr != nil {
		r.Error = "review process failed or timed out; fresh partial evidence retained"
	}
	r.MatchedIDs, r.MissedIDs, r.Unexpected = match(c.Expected, report.Findings)
	r.Passed = !r.Incomplete && len(r.MissedIDs) == 0 && len(r.Unexpected) == 0 && r.NeedsReview == 0 && r.ActualGate == r.ExpectedGate
	return r
}

func writeSources(repo string, files map[string]string) error {
	paths := make([]string, 0, len(files))
	for path := range files {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	for _, path := range paths {
		if !safeSourcePath(path) {
			return errors.New("unsafe source path")
		}
		destination := filepath.Join(repo, filepath.FromSlash(path))
		parent := repo
		segments := strings.Split(path, "/")
		for _, segment := range segments[:len(segments)-1] {
			parent = filepath.Join(parent, segment)
			info, err := os.Lstat(parent)
			if errors.Is(err, os.ErrNotExist) {
				if err := os.Mkdir(parent, 0700); err != nil {
					return err
				}
			} else if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
				return errors.New("fixture parent is not a regular directory")
			}
		}
		if info, err := os.Lstat(destination); err == nil && !info.Mode().IsRegular() {
			return errors.New("fixture destination is not a regular file")
		} else if err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if err := os.WriteFile(destination, []byte(files[path]), 0600); err != nil {
			return err
		}
	}
	return nil
}

func isolatedEnvironment(root string) []string {
	return []string{
		"PATH=" + os.Getenv("PATH"), "HOME=" + filepath.Join(root, "home"), "TMPDIR=" + filepath.Join(root, "tmp"), "TMP=" + filepath.Join(root, "tmp"), "TEMP=" + filepath.Join(root, "tmp"),
		"GOCACHE=" + filepath.Join(root, "go-cache"), "GOPATH=" + filepath.Join(root, "go-path"), "GOMODCACHE=" + filepath.Join(root, "go-mod"),
		"GOPROXY=off", "GOSUMDB=off", "GOTOOLCHAIN=local", "GOWORK=off", "GOENV=off", "GOFLAGS=-mod=readonly", "CGO_ENABLED=0",
		"GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_TERMINAL_PROMPT=0", "LC_ALL=C", "TZ=UTC",
		"GIT_AUTHOR_DATE=2000-01-01T00:00:00Z", "GIT_COMMITTER_DATE=2000-01-01T00:00:00Z",
	}
}

// Go's export-data format changes between releases. Using a newer local Go
// against a reviewer built by an older compiler can panic inside go/importer.
// Prefer explicitly configured installed tools, then PATH, and require an exact
// version match with the snapshotted reviewer instead of downloading anything.
func pinToolchain(ctx context.Context, root string, environment []string, required, configured string) ([]string, string, error) {
	candidates := []string{}
	if configured != "" {
		absolute, err := filepath.Abs(configured)
		if err != nil {
			return nil, "", fmt.Errorf("resolve --go-binary: %w", err)
		}
		candidates = append(candidates, absolute)
	} else {
		if goRoot := os.Getenv("GOROOT"); goRoot != "" {
			candidates = append(candidates, filepath.Join(goRoot, "bin", "go"))
		}
		if path, err := exec.LookPath("go"); err == nil {
			candidates = append(candidates, path)
		}
	}
	for _, candidate := range candidates {
		resolved, err := filepath.EvalSymlinks(candidate)
		if err != nil {
			continue
		}
		output, err := execute(ctx, filepath.Join(root, "control"), environment, resolved, "version")
		fields := strings.Fields(output)
		if err != nil || len(fields) < 3 || fields[2] != required {
			continue
		}
		goRoot, err := execute(ctx, filepath.Join(root, "control"), environment, resolved, "env", "GOROOT")
		if err != nil || strings.TrimSpace(goRoot) == "" {
			continue
		}
		result := append([]string{}, environment...)
		for index, entry := range result {
			if strings.HasPrefix(entry, "PATH=") {
				result[index] = "PATH=" + filepath.Dir(resolved) + string(os.PathListSeparator) + strings.TrimPrefix(entry, "PATH=")
			}
		}
		return append(result, "GOROOT="+strings.TrimSpace(goRoot)), resolved, nil
	}
	return nil, "", fmt.Errorf("live eval requires locally installed %s matching the reviewer binary; pass --go-binary /absolute/path/to/go or build Aegis with your installed Go version (automatic downloads are disabled)", required)
}

// Discard raw child stderr so model payloads and credentials cannot be copied
// into eval errors. A bounded stdout buffer is sufficient for Git object IDs.
func execute(ctx context.Context, directory string, environment []string, binary string, args ...string) (string, error) {
	// exec.Command resolves bare names against the parent PATH before Env is
	// assigned. Resolve explicitly against the isolated/pinned child PATH.
	if !filepath.IsAbs(binary) {
		resolved, err := environmentExecutable(binary, environment)
		if err != nil {
			return "", err
		}
		binary = resolved
	}
	// #nosec G702 G204 -- callers select only the explicitly trusted Go
	// evaluator snapshot, installed Go compiler, or Git from the pinned PATH.
	// Corpus contents never select an executable; argv uses fixed flags and
	// separate literal values, with no shell or command-string interpretation.
	command := exec.CommandContext(ctx, binary, args...)
	command.Dir = directory
	command.Env = environment
	command.WaitDelay = time.Second
	limit := &boundedOutput{}
	command.Stdout = limit
	stderr := &boundedOutput{}
	command.Stderr = stderr
	configureProcess(command)
	err := command.Run()
	if ctx.Err() != nil {
		err = ctx.Err()
	}
	if err != nil {
		for _, line := range strings.Split(stderr.String(), "\n") {
			if strings.HasPrefix(line, "panic: runtime error:") {
				err = &runtimeProcessError{cause: err, message: line}
				break
			}
		}
	}
	return limit.String(), err
}

func environmentExecutable(name string, environment []string) (string, error) {
	if strings.ContainsAny(name, "/\\") {
		return "", errors.New("child executable must be an absolute path or a bare command name")
	}
	for _, entry := range environment {
		if !strings.HasPrefix(entry, "PATH=") {
			continue
		}
		for _, directory := range filepath.SplitList(strings.TrimPrefix(entry, "PATH=")) {
			if !filepath.IsAbs(directory) {
				continue
			}
			candidate := filepath.Join(directory, name)
			if runtime.GOOS == "windows" {
				candidate += ".exe"
			}
			// #nosec G703 -- directory is an absolute entry from the trusted
			// maintainer/pinned toolchain PATH, not corpus input; the bare tool
			// name rejects path separators above, and this lookup is read-only.
			if info, err := os.Stat(candidate); err == nil && info.Mode().IsRegular() && (runtime.GOOS == "windows" || info.Mode()&0111 != 0) {
				return candidate, nil
			}
		}
	}
	return "", fmt.Errorf("required child executable %q was not found in the isolated PATH", name)
}

type runtimeProcessError struct {
	cause   error
	message string
}

func (e *runtimeProcessError) Error() string { return e.message }
func (e *runtimeProcessError) Unwrap() error { return e.cause }

func commandFailure(err error) string {
	if err == nil {
		return "report missing despite process success"
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "timeout"
	}
	if errors.Is(err, context.Canceled) {
		return "canceled"
	}
	var runtimeError *runtimeProcessError
	if errors.As(err, &runtimeError) {
		return runtimeError.message
	}
	var exitError *exec.ExitError
	if errors.As(err, &exitError) {
		return fmt.Sprintf("exit code %d", exitError.ExitCode())
	}
	return "process could not start"
}

type boundedOutput struct{ bytes.Buffer }

func (b *boundedOutput) Write(p []byte) (int, error) {
	n := len(p)
	available := 64*1024 - b.Len()
	if available > 0 {
		_, _ = b.Buffer.Write(p[:min(len(p), available)])
	}
	return n, nil
}
