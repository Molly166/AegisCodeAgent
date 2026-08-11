package main

import (
	"context"
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
	"github.com/Molly166/AegisCodeAgent/internal/gitdiff"
	"github.com/Molly166/AegisCodeAgent/internal/report"
	"github.com/Molly166/AegisCodeAgent/internal/review"
	"github.com/Molly166/AegisCodeAgent/internal/verifier"
)

const version = "0.5.0"

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
	base := flags.String("base", "main", "base Git revision")
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
	verifyAgentCandidates := flags.Bool("verify-agent-candidates", verificationDefaults.Enabled, "verify Agent candidates with local evidence and focused analyzers")
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
	if *contextMaxSymbols <= 0 || *contextMaxBytes <= 0 {
		fmt.Fprintln(stderr, "aegis: context-max-symbols and context-max-bytes must be positive")
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
	if len(selectedAnalyzers) > 0 || *contextEngine || reasoningProvider != nil {
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
			Repository: result.Repository,
			Packages:   contextPackages,
			Files:      result.Files,
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
	if *verifyAgentCandidates && (reviewReport.Agent.Status == review.AgentComplete || reviewReport.Agent.Status == review.AgentPartial) {
		verificationContext, cancel := context.WithTimeout(ctx, *verifierTimeout)
		verificationOutput, verifyErr := verifier.New(analyzer.NewDefaultPipeline(analyzer.OSRunner{})).Run(verificationContext, verifier.Config{
			Repository: result.Repository, AnalyzerTimeout: *verifierAnalyzerTimeout,
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

func writeRootUsage(output io.Writer) {
	fmt.Fprintln(output, "AegisCodeAgent — evidence-driven code review for Git changes")
	fmt.Fprintln(output)
	fmt.Fprintln(output, "Usage:")
	fmt.Fprintln(output, "  aegis review [flags]")
	fmt.Fprintln(output, "  aegis version")
	fmt.Fprintln(output)
	fmt.Fprintln(output, "Run 'aegis review --help' for review flags.")
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
