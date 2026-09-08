package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/Molly166/AegisCodeAgent/internal/githubreport"
	"github.com/Molly166/AegisCodeAgent/internal/liveeval"
)

func runEvalLive(ctx context.Context, arguments []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("eval-live", flag.ContinueOnError)
	flags.SetOutput(stderr)
	defaultBinary, _ := os.Executable()
	cfg := liveeval.Config{}
	flags.StringVar(&cfg.Corpus, "corpus", "eval/live", "trusted local synthetic corpus directory or corpus.json (fixture Go tests execute)")
	flags.StringVar(&cfg.Binary, "aegis-binary", defaultBinary, "trusted built Aegis executable used for every exact base/head review")
	flags.StringVar(&cfg.GoBinary, "go-binary", "", "trusted installed Go executable matching the reviewer build version (use for release/trimpath binaries when PATH differs)")
	flags.IntVar(&cfg.Repeats, "repeats", 1, "repeat each case 1..10 times; live model runs incur API usage")
	flags.DurationVar(&cfg.CaseTimeout, "case-timeout", 5*time.Minute, "maximum wall time per case including fixture setup")
	timeout := flags.Duration("timeout", 30*time.Minute, "total evaluation time budget")
	flags.StringVar(&cfg.Provider, "agent-provider", "none", "none, deepseek, orcarouter, or openai-compatible")
	flags.StringVar(&cfg.Model, "agent-model", "", "explicit model ID required when a model provider is enabled")
	flags.StringVar(&cfg.APIKeyEnv, "agent-api-key-env", "", "credential variable ending in _KEY; derived from provider by default")
	flags.StringVar(&cfg.BaseURL, "agent-base-url", "", "optional provider endpoint override (HTTPS enforced by reviewer)")
	flags.BoolVar(&cfg.AllowCustomEndpoint, "agent-allow-custom-endpoint", false, "allow sending fixture code and credentials to the selected custom endpoint")
	flags.StringVar(&cfg.Analyzers, "analyzers", "default", "default, all, none or comma-separated analyzer names")
	verifierEnabled := flags.Bool("verifier-enabled", true, "run Verifier; false is an explicit ablation and does not promote unverified model candidates")
	format := flags.String("format", "html", "html or json")
	output := flags.String("output", "eval-live-report.html", "output file path, or - for stdout")
	strict := flags.Bool("strict", false, "exit non-zero on any failed or incomplete case; baseline misses are expected")
	failOn := flags.String("fail-on", "p1", "finding merge-gate threshold")
	needsReview := flags.String("fail-on-needs-review", "p0", "unresolved-hypothesis merge-gate threshold")
	flags.BoolVar(&cfg.Gate.FailOnIncomplete, "fail-on-incomplete", true, "block incomplete mandatory review stages")
	flags.Usage = func() {
		fmt.Fprintln(stderr, "Execute trusted synthetic Git fixtures through the complete review pipeline.\nUsage: aegis eval-live [flags]")
		flags.PrintDefaults()
	}
	if err := flags.Parse(arguments); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if flags.NArg() != 0 || *timeout <= 0 || (*format != "html" && *format != "json") {
		fmt.Fprintln(stderr, "aegis: invalid eval-live arguments, timeout, or format")
		return 2
	}
	var err error
	cfg.DisableVerifier = !*verifierEnabled
	if cfg.Gate.FailOn, err = githubreport.ParsePriority(*failOn); err != nil {
		fmt.Fprintf(stderr, "aegis: %v\n", err)
		return 2
	}
	if cfg.Gate.FailOnNeedsReview, err = githubreport.ParsePriority(*needsReview); err != nil {
		fmt.Fprintf(stderr, "aegis: %v\n", err)
		return 2
	}
	runCtx, cancel := context.WithTimeout(ctx, *timeout)
	defer cancel()
	result, err := liveeval.Run(runCtx, cfg)
	if err != nil {
		fmt.Fprintf(stderr, "aegis: eval-live: %v\n", err)
		return 1
	}
	encoded, err := liveeval.Render(*format, result)
	if err != nil {
		fmt.Fprintf(stderr, "aegis: render eval-live: %v\n", err)
		return 1
	}
	if err := writeOutput(*output, encoded, stdout); err != nil {
		fmt.Fprintf(stderr, "aegis: %v\n", err)
		return 1
	}
	fmt.Fprintf(stderr, "aegis eval-live: %d/%d runs passed; %d incomplete; provider=%s; live-model-requested=%t\n", result.Metrics.PassedRuns, result.Metrics.Runs, result.Metrics.IncompleteRate.Numerator, result.Provider, result.LiveModel)
	if runCtx.Err() != nil || (*strict && result.Metrics.PassedRuns != result.Metrics.Runs) {
		return 1
	}
	return 0
}
