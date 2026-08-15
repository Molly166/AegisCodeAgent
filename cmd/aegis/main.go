package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Molly166/AegisCodeAgent/internal/agent"
	"github.com/Molly166/AegisCodeAgent/internal/analyzer"
	appconfig "github.com/Molly166/AegisCodeAgent/internal/config"
	repocontext "github.com/Molly166/AegisCodeAgent/internal/context"
	evalharness "github.com/Molly166/AegisCodeAgent/internal/eval"
	"github.com/Molly166/AegisCodeAgent/internal/gitdiff"
	"github.com/Molly166/AegisCodeAgent/internal/githubreport"
	"github.com/Molly166/AegisCodeAgent/internal/report"
	"github.com/Molly166/AegisCodeAgent/internal/review"
	"github.com/Molly166/AegisCodeAgent/internal/verifier"
)

const (
	version            = "0.7.0"
	maxReviewJSONBytes = 32 * 1024 * 1024
)

func main() {
	os.Exit(run(context.Background(), os.Args[1:], os.Stdout, os.Stderr))
}

func run(ctx context.Context, arguments []string, stdout, stderr io.Writer) int {
	if len(arguments) == 0 {
		writeRootUsage(stderr)
		return 2
	}

	switch arguments[0] {
	case "review":
		return runReview(ctx, arguments[1:], stdout, stderr)
	case "github":
		return runGitHub(arguments[1:], stdout, stderr)
	case "eval":
		return runEval(arguments[1:], stdout, stderr)
	case "version", "--version", "-version":
		fmt.Fprintf(stdout, "aegis %s\n", version)
		return 0
	case "help", "--help", "-h":
		writeRootUsage(stdout)
		return 0
	default:
		fmt.Fprintf(stderr, "unknown command %q\n\n", arguments[0])
		writeRootUsage(stderr)
		return 2
	}
}

func runEval(arguments []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("eval", flag.ContinueOnError)
	flags.SetOutput(stderr)
	corpus := flags.String("corpus", "eval/cases", "path to a replay eval corpus")
	format := flags.String("format", evalharness.FormatHTML, "eval report format: html or json")
	outputPath := flags.String("output", "eval-report.html", "output file path, or - for stdout")
	failOnValue := flags.String("fail-on", "p1", "finding merge-gate threshold")
	failOnNeedsReviewValue := flags.String("fail-on-needs-review", "p0", "unresolved-hypothesis merge-gate threshold")
	failOnIncomplete := flags.Bool("fail-on-incomplete", true, "treat incomplete review stages as blocked")
	strict := flags.Bool("strict", true, "exit non-zero when any eval case fails")
	flags.Usage = func() { writeEvalUsage(stderr, flags) }
	if err := flags.Parse(arguments); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintf(stderr, "eval does not accept positional arguments: %v\n", flags.Args())
		return 2
	}
	failOn, err := githubreport.ParsePriority(*failOnValue)
	if err != nil {
		fmt.Fprintf(stderr, "aegis: %v\n", err)
		return 2
	}
	failOnNeedsReview, err := githubreport.ParsePriority(*failOnNeedsReviewValue)
	if err != nil {
		fmt.Fprintf(stderr, "aegis: %v\n", err)
		return 2
	}
	evalReport, err := evalharness.Run(evalharness.Config{
		Corpus: *corpus,
		Gate: githubreport.Options{
			FailOn: failOn, FailOnNeedsReview: failOnNeedsReview, FailOnIncomplete: *failOnIncomplete,
		},
	})
	if err != nil {
		fmt.Fprintf(stderr, "aegis: run eval: %v\n", err)
		return 1
	}
	encoded, err := evalharness.Render(*format, evalReport)
	if err != nil {
		fmt.Fprintf(stderr, "aegis: %v\n", err)
		return 2
	}
	if err := writeOutput(*outputPath, encoded, stdout); err != nil {
		fmt.Fprintf(stderr, "aegis: %v\n", err)
		return 1
	}
	fmt.Fprintf(stderr, "aegis eval: %d/%d cases passed, precision %.1f%%, recall %.1f%%, gate accuracy %.1f%%\n",
		evalReport.Metrics.PassedCases, evalReport.Metrics.Cases,
		evalReport.Metrics.Precision*100, evalReport.Metrics.Recall*100, evalReport.Metrics.GateAccuracy*100)
	if *strict && evalReport.Metrics.PassedCases != evalReport.Metrics.Cases {
		return 1
	}
	return 0
}

func runGitHub(arguments []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("github", flag.ContinueOnError)
	flags.SetOutput(stderr)
	reportPath := flags.String("report", "review.json", "path to an Aegis JSON review report")
	htmlOutput := flags.String("html-output", "review.html", "path for the self-contained HTML evidence report")
	summaryPath := flags.String("summary", os.Getenv("GITHUB_STEP_SUMMARY"), "path to the GitHub step summary file")
	annotations := flags.Bool("annotations", true, "emit GitHub workflow annotations to stdout")
	failOnValue := flags.String("fail-on", "p1", "merge gate threshold: p0, p1, p2, p3, or none")
	failOnNeedsReviewValue := flags.String("fail-on-needs-review", "p0", "merge gate threshold for unresolved hypotheses: p0, p1, p2, p3, or none")
	failOnIncomplete := flags.Bool("fail-on-incomplete", true, "fail the merge gate when a requested review stage is partial or failed")
	maxAnnotations := flags.Int("max-annotations", githubreport.DefaultMaxAnnotations, "maximum line annotations emitted per run")
	artifactName := flags.String("artifact-name", "aegis-review-report", "artifact name referenced by the GitHub summary")
	flags.Usage = func() { writeGitHubUsage(stderr, flags) }
	if err := flags.Parse(arguments); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintf(stderr, "github does not accept positional arguments: %v\n", flags.Args())
		return 2
	}
	failOn, err := githubreport.ParsePriority(*failOnValue)
	if err != nil {
		fmt.Fprintf(stderr, "aegis: %v\n", err)
		return 2
	}
	failOnNeedsReview, err := githubreport.ParsePriority(*failOnNeedsReviewValue)
	if err != nil {
		fmt.Fprintf(stderr, "aegis: %v\n", err)
		return 2
	}
	if *maxAnnotations <= 0 {
		fmt.Fprintln(stderr, "aegis: max-annotations must be positive")
		return 2
	}
	if strings.TrimSpace(*summaryPath) == "" {
		fmt.Fprintln(stderr, "aegis: summary path is required; pass --summary or run inside GitHub Actions")
		return 2
	}

	reviewReport, err := loadJSONReport(*reportPath)
	if err != nil {
		fmt.Fprintf(stderr, "aegis: %v\n", err)
		return 1
	}
	htmlReport, err := report.RenderHTML(reviewReport)
	if err != nil {
		fmt.Fprintf(stderr, "aegis: %v\n", err)
		return 1
	}
	if err := writeOutput(*htmlOutput, htmlReport, stdout); err != nil {
		fmt.Fprintf(stderr, "aegis: %v\n", err)
		return 1
	}
	summary := githubreport.RenderSummary(reviewReport, githubreport.Options{
		FailOn: failOn, FailOnNeedsReview: failOnNeedsReview,
		FailOnIncomplete: *failOnIncomplete, ArtifactName: *artifactName,
	})
	if err := appendOutput(*summaryPath, summary); err != nil {
		fmt.Fprintf(stderr, "aegis: %v\n", err)
		return 1
	}
	if *annotations {
		if _, err := stdout.Write(githubreport.RenderAnnotations(reviewReport, *maxAnnotations)); err != nil {
			fmt.Fprintf(stderr, "aegis: write GitHub annotations: %v\n", err)
			return 1
		}
	}
	gate := githubreport.EvaluateWithOptions(reviewReport, githubreport.Options{
		FailOn: failOn, FailOnNeedsReview: failOnNeedsReview, FailOnIncomplete: *failOnIncomplete,
	})
	if gate.Blocked {
		if gate.Incomplete {
			fmt.Fprintln(stderr, "aegis: GitHub merge gate blocked because the review is incomplete")
		} else if gate.BlockedByFinding {
			fmt.Fprintf(stderr, "aegis: GitHub merge gate blocked by %s finding(s)\n", strings.ToUpper(string(gate.Highest)))
		} else if gate.BlockedByNeedsReview {
			fmt.Fprintf(stderr, "aegis: GitHub merge gate blocked by unresolved %s hypothesis\n", strings.ToUpper(string(gate.NeedsReviewHighest)))
		}
		return 1
	}
	return 0
}

func runReview(ctx context.Context, arguments []string, stdout, stderr io.Writer) int {
	fileConfig, configPath, configErr := loadReviewConfig(arguments)
	if configErr != nil {
		fmt.Fprintf(stderr, "aegis: %v\n", configErr)
		return 2
	}
	agentDefaults, defaultsErr := reviewAgentDefaults(fileConfig.Agent)
	if defaultsErr != nil {
		fmt.Fprintf(stderr, "aegis: %v\n", defaultsErr)
		return 2
	}
	verificationDefaults, defaultsErr := reviewVerifierDefaults(fileConfig.Verifier)
	if defaultsErr != nil {
		fmt.Fprintf(stderr, "aegis: %v\n", defaultsErr)
		return 2
	}

	flags := flag.NewFlagSet("review", flag.ContinueOnError)
	flags.SetOutput(stderr)
	configurationPath := flags.String("config", configPath, "path to an Aegis JSON config file")
	repository := flags.String("repo", ".", "path to the Git repository")
	base := flags.String("base", "master", "base Git revision")
	head := flags.String("head", "HEAD", "head Git revision")
	format := flags.String("format", report.FormatHTML, "report format: html, markdown, or json")
	outputPath := flags.String("output", "-", "output file path, or - for stdout")
	contextLines := flags.Int("context", 3, "number of context lines per diff hunk")
	timeout := flags.Duration("timeout", 30*time.Second, "maximum time for Git operations")
	analyzerSelection := flags.String("analyzers", "default", "comma-separated analyzers, default, all, or none")
	analysisScope := flags.String("analysis-scope", analyzer.ScopeChanged, "package scope: changed or all")
	changedLinesOnly := flags.Bool("changed-lines-only", true, "publish static diagnostics only on added lines")
	analyzerTimeout := flags.Duration("analyzer-timeout", 2*time.Minute, "maximum time for each analyzer")
	allowDirtyAnalysis := flags.Bool("allow-dirty-analysis", false, "allow analyzers to run on a dirty or mismatched worktree")
	contextEngine := flags.Bool("repo-context", true, "build repository context for changed Go symbols")
	contextScope := flags.String("context-scope", analyzer.ScopeAll, "repository context scope: changed or all")
	contextMaxSymbols := flags.Int("context-max-symbols", 40, "maximum changed and related symbols in the context bundle")
	contextMaxBytes := flags.Int("context-max-bytes", 48*1024, "maximum approximate context payload size in bytes")
	contextIntentMaxBytes := flags.Int("context-intent-max-bytes", 24*1024, "maximum PR intent and repository-guidance payload size")
	githubEventPath := flags.String("github-event", os.Getenv("GITHUB_EVENT_PATH"), "path to a GitHub event JSON file used to extract pull-request intent")
	contextTimeout := flags.Duration("context-timeout", time.Minute, "maximum time for repository context indexing")
	agentProviderName := flags.String("agent-provider", agentDefaults.Provider, "reasoning agent provider: none or deepseek")
	agentModel := flags.String("agent-model", agentDefaults.Model, "reasoning model name")
	agentBaseURL := flags.String("agent-base-url", agentDefaults.BaseURL, "provider API base URL (HTTPS required)")
	agentAPIKeyEnv := flags.String("agent-api-key-env", agentDefaults.APIKeyEnv, "environment variable containing the provider API key")
	agentAllowCustomEndpoint := flags.Bool("agent-allow-custom-endpoint", false, "allow sending code and the API key to a non-official provider endpoint")
	agentThinking := flags.Bool("agent-thinking", agentDefaults.Thinking, "enable model thinking mode")
	agentReasoningEffort := flags.String("agent-reasoning-effort", agentDefaults.ReasoningEffort, "thinking effort: low, medium, high, xhigh, or max")
	agentTimeout := flags.Duration("agent-timeout", agentDefaults.Timeout, "maximum time for the complete reasoning loop")
	agentMaxSteps := flags.Int("agent-max-steps", agentDefaults.MaxSteps, "maximum model turns in the reasoning loop")
	agentMaxCandidates := flags.Int("agent-max-candidates", agentDefaults.MaxCandidates, "maximum unverified candidates accepted from the model")
	agentMaxInputBytes := flags.Int("agent-max-input-bytes", agentDefaults.MaxInputBytes, "maximum serialized diff/context bytes sent to the model")
	agentMaxOutputTokens := flags.Int("agent-max-output-tokens", agentDefaults.MaxOutputTokens, "maximum output tokens per model turn")
	verifyAgentCandidates := flags.Bool("verify-agent-candidates", verificationDefaults.Enabled, "run semantic evidence rules and verify Agent candidates with focused analyzers")
	verifierTimeout := flags.Duration("verifier-timeout", verificationDefaults.Timeout, "maximum time for the complete verification stage")
	verifierAnalyzerTimeout := flags.Duration("verifier-analyzer-timeout", verificationDefaults.AnalyzerTimeout, "maximum time for each focused verification analyzer")
	flags.Usage = func() { writeReviewUsage(stderr, flags) }

	if err := flags.Parse(arguments); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintf(stderr, "review does not accept positional arguments: %v\n", flags.Args())
		return 2
	}
	if !report.IsSupported(*format) {
		fmt.Fprintf(stderr, "aegis: unsupported report format %q (supported: html, markdown, json)\n", *format)
		return 2
	}
	selectedAnalyzers, err := analyzer.ParseSelection(*analyzerSelection)
	if err != nil {
		fmt.Fprintf(stderr, "aegis: %v\n", err)
		return 2
	}
	if err := analyzer.ValidateScope(*analysisScope); err != nil {
		fmt.Fprintf(stderr, "aegis: %v\n", err)
		return 2
	}
	if err := analyzer.ValidateScope(*contextScope); err != nil {
		fmt.Fprintf(stderr, "aegis: invalid context scope: %v\n", err)
		return 2
	}
	if *contextMaxSymbols <= 0 || *contextMaxBytes <= 0 || *contextIntentMaxBytes <= 0 {
		fmt.Fprintln(stderr, "aegis: context-max-symbols, context-max-bytes, and context-intent-max-bytes must be positive")
		return 2
	}
	if err := agent.ValidateProvider(*agentProviderName); err != nil {
		fmt.Fprintf(stderr, "aegis: %v\n", err)
		return 2
	}
	agentConfiguration, err := agent.NormalizeConfig(agent.Config{
		Repository: *repository, Model: *agentModel, Thinking: *agentThinking,
		ReasoningEffort: *agentReasoningEffort, MaxSteps: *agentMaxSteps,
		MaxCandidates: *agentMaxCandidates, MaxInputBytes: *agentMaxInputBytes,
		MaxOutputTokens: *agentMaxOutputTokens,
	})
	if err != nil {
		fmt.Fprintf(stderr, "aegis: %v\n", err)
		return 2
	}
	if *agentTimeout <= 0 {
		fmt.Fprintln(stderr, "aegis: agent-timeout must be positive")
		return 2
	}
	if *verifierTimeout <= 0 || *verifierAnalyzerTimeout <= 0 {
		fmt.Fprintln(stderr, "aegis: verifier-timeout and verifier-analyzer-timeout must be positive")
		return 2
	}
	var reasoningProvider agent.Provider
	if strings.EqualFold(*agentProviderName, agent.ProviderDeepSeek) {
		if *agentAPIKeyEnv != "DEEPSEEK_API_KEY" {
			fmt.Fprintln(stderr, "aegis: DeepSeek provider only accepts DEEPSEEK_API_KEY as its credential variable")
			return 2
		}
		dotEnvPath := filepath.Join(*repository, ".env")
		if *configurationPath != "" {
			dotEnvPath = filepath.Join(filepath.Dir(*configurationPath), ".env")
		}
		apiKey, keyErr := appconfig.APIKey(dotEnvPath, *agentAPIKeyEnv)
		if keyErr != nil {
			fmt.Fprintf(stderr, "aegis: %v\n", keyErr)
			return 2
		}
		if apiKey == "" {
			fmt.Fprintf(stderr, "aegis: DeepSeek API key is missing; set %s or add it to %s\n", *agentAPIKeyEnv, dotEnvPath)
			return 2
		}
		reasoningProvider, err = agent.NewDeepSeekProvider(agent.DeepSeekConfig{
			APIKey: apiKey, BaseURL: *agentBaseURL, AllowCustomEndpoint: *agentAllowCustomEndpoint,
		})
		if err != nil {
			fmt.Fprintf(stderr, "aegis: configure DeepSeek provider: %v\n", err)
			return 2
		}
	}

	client := gitdiff.NewClient(*timeout)
	result, err := client.Collect(ctx, gitdiff.Options{
		Repository:   *repository,
		Base:         *base,
		Head:         *head,
		ContextLines: *contextLines,
	})
	if err != nil {
		fmt.Fprintf(stderr, "aegis: %v\n", err)
		return 1
	}

	comparison := review.Comparison{
		Repository: result.Repository,
		Base:       result.Base,
		Head:       result.Head,
		BaseCommit: result.BaseCommit,
		HeadCommit: result.HeadCommit,
	}
	reviewReport := review.NewScopeReport(comparison, result.Files)
	if len(selectedAnalyzers) > 0 || *contextEngine || reasoningProvider != nil || *verifyAgentCandidates {
		if !*allowDirtyAnalysis && (result.WorktreeCommit != result.HeadCommit || !result.WorktreeClean) {
			fmt.Fprintln(stderr, "aegis: analysis/context worktree does not exactly match the requested head revision")
			fmt.Fprintln(stderr, "aegis: check out the requested head and clean/stash local changes, or explicitly use --allow-dirty-analysis")
			return 1
		}
	}
	if len(selectedAnalyzers) > 0 {
		packages, err := analyzer.SelectPackages(result.Repository, result.Files, *analysisScope)
		if err != nil {
			fmt.Fprintf(stderr, "aegis: select analysis packages: %v\n", err)
			return 1
		}
		pipeline := analyzer.NewDefaultPipeline(analyzer.OSRunner{})
		analysisOutput, err := pipeline.Run(ctx, analyzer.Input{
			Repository:   result.Repository,
			Packages:     packages,
			ChangedLines: analyzer.BuildChangedLineSet(result.Repository, result.Files),
		}, selectedAnalyzers, analyzer.RunOptions{
			OnlyChangedLines: *changedLinesOnly,
			AnalyzerTimeout:  *analyzerTimeout,
		})
		if err != nil {
			fmt.Fprintf(stderr, "aegis: run analysis pipeline: %v\n", err)
			return 1
		}
		reviewReport = review.NewReport(comparison, result.Files, analysisOutput.Findings)
		reviewReport.Analysis = analysisOutput.Analysis
	}
	if *contextEngine {
		contextPackages := []string{"./..."}
		if *contextScope == analyzer.ScopeChanged {
			contextPackages, err = analyzer.SelectPackages(result.Repository, result.Files, analyzer.ScopeChanged)
			if err != nil {
				fmt.Fprintf(stderr, "aegis: select context packages: %v\n", err)
				return 1
			}
		}
		indexContext, cancel := context.WithTimeout(ctx, *contextTimeout)
		contextBundle, contextErr := repocontext.NewBuilder(analyzer.OSRunner{MaxOutputBytes: 16 * 1024 * 1024}).Build(indexContext, repocontext.Input{
			Repository:      result.Repository,
			Packages:        contextPackages,
			Files:           result.Files,
			GitHubEventPath: *githubEventPath,
			IntentMaxBytes:  *contextIntentMaxBytes,
			Budget: repocontext.Budget{
				MaxSymbols:    *contextMaxSymbols,
				MaxTotalBytes: *contextMaxBytes,
			},
		})
		cancel()
		if contextErr != nil {
			contextBundle = review.EmptyContextBundle(review.ContextFailed)
			contextBundle.Warnings = append(contextBundle.Warnings, contextErr.Error())
		}
		reviewReport.Context = contextBundle
	}
	var agentRunErr error
	if reasoningProvider != nil {
		repositoryTools, toolErr := agent.NewRepositoryTools(result.Repository)
		if toolErr != nil {
			agentRunErr = toolErr
			reviewReport.Agent = review.EmptyAgentRun(review.AgentFailed)
			reviewReport.Agent.Provider = reasoningProvider.Name()
			reviewReport.Agent.Model = agentConfiguration.Model
			reviewReport.Agent.Thinking = agentConfiguration.Thinking
			reviewReport.Agent.Warnings = append(reviewReport.Agent.Warnings, toolErr.Error())
		} else {
			agentConfiguration.Repository = result.Repository
			agentContext, cancel := context.WithTimeout(ctx, *agentTimeout)
			reviewReport.Agent, agentRunErr = agent.NewRunner(reasoningProvider, repositoryTools).Run(agentContext, agentConfiguration, agent.RunInput{
				Comparison: comparison, Files: result.Files, StaticFindings: reviewReport.Findings, Context: reviewReport.Context,
			})
			cancel()
		}
	}
	var verificationErr error
	if *verifyAgentCandidates {
		verificationContext, cancel := context.WithTimeout(ctx, *verifierTimeout)
		verificationOutput, verifyErr := verifier.New(analyzer.NewDefaultPipeline(analyzer.OSRunner{})).Run(verificationContext, verifier.Config{
			Repository: result.Repository, AnalyzerTimeout: *verifierAnalyzerTimeout, AnalyzerNames: selectedAnalyzers,
		}, verifier.Input{
			Files: result.Files, Findings: reviewReport.Findings, Agent: reviewReport.Agent,
		})
		cancel()
		verificationErr = verifyErr
		reviewReport.Verification = verificationOutput.Verification
		reviewReport.Findings = verifier.MergeFindings(reviewReport.Findings, verificationOutput.PromotedFindings)
		reviewReport.RecalculateSummary()
	}
	encoded, err := report.Render(*format, reviewReport)
	if err != nil {
		fmt.Fprintf(stderr, "aegis: %v\n", err)
		return 1
	}
	if err := writeOutput(*outputPath, encoded, stdout); err != nil {
		fmt.Fprintf(stderr, "aegis: %v\n", err)
		return 1
	}
	if reviewReport.Analysis.Status == review.AnalysisPartial {
		fmt.Fprintln(stderr, "aegis: analysis is partial; inspect analyzer status in the report")
	}
	if reviewReport.Analysis.Status == review.AnalysisFailed {
		fmt.Fprintln(stderr, "aegis: analysis failed; the report was written with diagnostic details")
		return 1
	}
	if reviewReport.Context.Status == review.ContextPartial {
		fmt.Fprintln(stderr, "aegis: repository context is partial; inspect warnings in the report")
	}
	if reviewReport.Context.Status == review.ContextFailed {
		fmt.Fprintln(stderr, "aegis: repository context failed; the report was written with diagnostic details")
		return 1
	}
	if reviewReport.Agent.Status == review.AgentPartial {
		fmt.Fprintln(stderr, "aegis: reasoning agent is partial; inspect its warnings and returned candidates")
	}
	if reviewReport.Agent.Status == review.AgentFailed || agentRunErr != nil {
		fmt.Fprintln(stderr, "aegis: reasoning agent failed; the report was written with diagnostic details")
		return 1
	}
	if reviewReport.Verification.Status == review.VerificationPartial {
		fmt.Fprintln(stderr, "aegis: verification is partial; inspect focused tools and candidate verdicts")
	}
	if reviewReport.Verification.Status == review.VerificationFailed || verificationErr != nil {
		fmt.Fprintln(stderr, "aegis: verification failed; the report was written with diagnostic details")
		return 1
	}
	return 0
}

type agentFlagDefaults struct {
	Provider        string
	Model           string
	BaseURL         string
	APIKeyEnv       string
	Thinking        bool
	ReasoningEffort string
	Timeout         time.Duration
	MaxSteps        int
	MaxCandidates   int
	MaxInputBytes   int
	MaxOutputTokens int
}

type verifierFlagDefaults struct {
	Enabled         bool
	Timeout         time.Duration
	AnalyzerTimeout time.Duration
}

func reviewVerifierDefaults(configuration appconfig.VerifierConfig) (verifierFlagDefaults, error) {
	defaults := verifierFlagDefaults{Enabled: true, Timeout: 3 * time.Minute, AnalyzerTimeout: 2 * time.Minute}
	if configuration.Enabled != nil {
		defaults.Enabled = *configuration.Enabled
	}
	if configuration.Timeout != "" {
		parsed, err := time.ParseDuration(configuration.Timeout)
		if err != nil {
			return verifierFlagDefaults{}, fmt.Errorf("invalid verifier timeout in config: %w", err)
		}
		defaults.Timeout = parsed
	}
	if configuration.AnalyzerTimeout != "" {
		parsed, err := time.ParseDuration(configuration.AnalyzerTimeout)
		if err != nil {
			return verifierFlagDefaults{}, fmt.Errorf("invalid verifier analyzer timeout in config: %w", err)
		}
		defaults.AnalyzerTimeout = parsed
	}
	return defaults, nil
}

func reviewAgentDefaults(configuration appconfig.AgentConfig) (agentFlagDefaults, error) {
	defaults := agentFlagDefaults{
		Provider: agent.ProviderNone, Model: agent.DefaultModel, BaseURL: agent.DefaultDeepSeekBaseURL,
		APIKeyEnv: "DEEPSEEK_API_KEY", Thinking: true, ReasoningEffort: "high",
		Timeout: 3 * time.Minute, MaxSteps: 6, MaxCandidates: 12,
		MaxInputBytes: 96 * 1024, MaxOutputTokens: 8192,
	}
	if configuration.Provider != "" {
		defaults.Provider = configuration.Provider
	}
	if configuration.Model != "" {
		defaults.Model = configuration.Model
	}
	if configuration.BaseURL != "" {
		defaults.BaseURL = configuration.BaseURL
	}
	if configuration.APIKeyEnv != "" {
		defaults.APIKeyEnv = configuration.APIKeyEnv
	}
	if configuration.Thinking != nil {
		defaults.Thinking = *configuration.Thinking
	}
	if configuration.ReasoningEffort != "" {
		defaults.ReasoningEffort = configuration.ReasoningEffort
	}
	if configuration.Timeout != "" {
		parsed, err := time.ParseDuration(configuration.Timeout)
		if err != nil {
			return agentFlagDefaults{}, fmt.Errorf("invalid agent timeout in config: %w", err)
		}
		defaults.Timeout = parsed
	}
	if configuration.MaxSteps != 0 {
		defaults.MaxSteps = configuration.MaxSteps
	}
	if configuration.MaxCandidates != 0 {
		defaults.MaxCandidates = configuration.MaxCandidates
	}
	if configuration.MaxInputBytes != 0 {
		defaults.MaxInputBytes = configuration.MaxInputBytes
	}
	if configuration.MaxOutputTokens != 0 {
		defaults.MaxOutputTokens = configuration.MaxOutputTokens
	}
	return defaults, nil
}

func loadReviewConfig(arguments []string) (appconfig.File, string, error) {
	configPath, explicit, err := argumentValue(arguments, "config", "")
	if err != nil {
		return appconfig.File{}, "", err
	}
	if !explicit {
		return appconfig.File{}, "", nil
	}
	if configPath == "" {
		return appconfig.File{}, "", nil
	}
	loaded, err := appconfig.Load(configPath)
	if err != nil {
		return appconfig.File{}, "", err
	}
	return loaded, configPath, nil
}

func argumentValue(arguments []string, name, fallback string) (string, bool, error) {
	names := []string{"--" + name, "-" + name}
	value, found := fallback, false
	for index := 0; index < len(arguments); index++ {
		argument := arguments[index]
		if argument == "--" {
			break
		}
		for _, flagName := range names {
			if argument == flagName {
				if index+1 >= len(arguments) {
					return "", false, fmt.Errorf("flag needs an argument: %s", flagName)
				}
				value, found = arguments[index+1], true
				index++
				break
			}
			if strings.HasPrefix(argument, flagName+"=") {
				value, found = strings.TrimPrefix(argument, flagName+"="), true
				break
			}
		}
	}
	return value, found, nil
}

func writeOutput(path string, content []byte, stdout io.Writer) error {
	if path == "-" {
		_, err := stdout.Write(content)
		return err
	}
	cleanPath := filepath.Clean(path)
	if err := os.WriteFile(cleanPath, content, 0o644); err != nil {
		return fmt.Errorf("write report to %q: %w", cleanPath, err)
	}
	return nil
}

func appendOutput(path string, content []byte) error {
	cleanPath := filepath.Clean(path)
	file, err := os.OpenFile(cleanPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return fmt.Errorf("open GitHub summary %q: %w", cleanPath, err)
	}
	defer file.Close()
	if _, err := file.Write(content); err != nil {
		return fmt.Errorf("write GitHub summary %q: %w", cleanPath, err)
	}
	return nil
}

func loadJSONReport(path string) (review.ReviewReport, error) {
	file, err := os.Open(filepath.Clean(path))
	if err != nil {
		return review.ReviewReport{}, fmt.Errorf("open JSON report %q: %w", path, err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return review.ReviewReport{}, fmt.Errorf("inspect JSON report %q: %w", path, err)
	}
	if !info.Mode().IsRegular() || info.Size() > maxReviewJSONBytes {
		return review.ReviewReport{}, fmt.Errorf("JSON report %q must be a regular file no larger than %d bytes", path, maxReviewJSONBytes)
	}
	decoder := json.NewDecoder(io.LimitReader(file, maxReviewJSONBytes+1))
	decoder.DisallowUnknownFields()
	var result review.ReviewReport
	if err := decoder.Decode(&result); err != nil {
		return review.ReviewReport{}, fmt.Errorf("decode JSON report %q: %w", path, err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			err = errors.New("multiple JSON values are not allowed")
		}
		return review.ReviewReport{}, fmt.Errorf("decode JSON report %q: %w", path, err)
	}
	if err := review.UpgradeReport(&result); err != nil {
		return review.ReviewReport{}, fmt.Errorf("JSON report: %w", err)
	}
	return result, nil
}

func writeRootUsage(output io.Writer) {
	fmt.Fprintln(output, "AegisCodeAgent — evidence-driven code review for Git changes")
	fmt.Fprintln(output)
	fmt.Fprintln(output, "Usage:")
	fmt.Fprintln(output, "  aegis review [flags]")
	fmt.Fprintln(output, "  aegis github [flags]")
	fmt.Fprintln(output, "  aegis eval [flags]")
	fmt.Fprintln(output, "  aegis version")
	fmt.Fprintln(output)
	fmt.Fprintln(output, "Run 'aegis <command> --help' for command flags.")
}

func writeEvalUsage(output io.Writer, flags *flag.FlagSet) {
	fmt.Fprintln(output, "Evaluate saved Aegis review reports against a versioned regression corpus.")
	fmt.Fprintln(output)
	fmt.Fprintln(output, "Usage:")
	fmt.Fprintln(output, "  aegis eval [flags]")
	fmt.Fprintln(output)
	fmt.Fprintln(output, "Flags:")
	flags.PrintDefaults()
}

func writeGitHubUsage(output io.Writer, flags *flag.FlagSet) {
	fmt.Fprintln(output, "Publish an Aegis JSON report as GitHub Summary, annotations, HTML, and a merge gate.")
	fmt.Fprintln(output)
	fmt.Fprintln(output, "Usage:")
	fmt.Fprintln(output, "  aegis github [flags]")
	fmt.Fprintln(output)
	fmt.Fprintln(output, "Flags:")
	flags.PrintDefaults()
}

func writeReviewUsage(output io.Writer, flags *flag.FlagSet) {
	fmt.Fprintln(output, "Review the changes between two Git revisions.")
	fmt.Fprintln(output)
	fmt.Fprintln(output, "Usage:")
	fmt.Fprintln(output, "  aegis review [flags]")
	fmt.Fprintln(output)
	fmt.Fprintln(output, "Flags:")
	flags.PrintDefaults()
}
