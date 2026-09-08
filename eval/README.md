# Aegis Eval Corpus

This directory contains the versioned **Golden Regression Corpus** for Aegis review reports and merge-gate behavior. It is designed to make validation reproducible and auditable; it is not presented as a live-model benchmark.

## Corpus composition

The v2 corpus contains exactly 50 cases:

| Case kind | Count | Purpose |
| --- | ---: | --- |
| Bug | 27 | Match recorded P0-P3 findings and verify severity-aware gate behavior |
| Clean | 15 | Measure false blocks on negative controls |
| Needs Review | 5 | Preserve unresolved hypotheses and exercise the independent P0 review threshold |
| Resilience | 3 | Verify partial-stage and degraded-Agent gate semantics |

Finding distribution:

| Priority | Expected findings | Default gate |
| --- | ---: | --- |
| P0 | 9 | Block |
| P1 | 9 | Block |
| P2 | 6 | Report only |
| P3 | 3 | Report only |

The corpus exercises `static`, `semantic`, `agent`, `context`, `mixed`, and `gate` evaluation layers. Four cases are regressions derived from failures observed while building Aegis; 46 are explicitly labeled `curated_synthetic` negative or positive controls.

## Files and provenance

- `catalog.json` is the human-reviewable source of truth for scenario labels and distribution.
- `cases/<id>/case.json` is the generated v2 expectation contract.
- `cases/<id>/report.json` is the generated, versioned Aegis v6 golden report.
- `tools/evalcorpus` validates the catalog and regenerates case artifacts atomically.

Every case declares:

- `kind`: `bug`, `clean`, `needs_review`, or `resilience`;
- `layer`: the part of the review system under evaluation;
- `provenance`: `regression` or `curated_synthetic`;
- the expected finding contract, merge-gate outcome, and unresolved-hypothesis count.

The Harness rejects weak bug labels: each expected finding must include severity, category, path, positive line, rule ID, and title constraint. Clean, Needs Review, and resilience cases have mutually exclusive contracts so they cannot silently drift into one another.

## Run the corpus

```bash
go run ./cmd/aegis eval \
  --corpus ./eval/cases \
  --format html \
  --output ./eval-report.html
```

Strict mode exits non-zero when a finding is missed, an unexpected finding exceeds the allowance, a Needs Review count changes, or the merge-gate result is wrong.

Regenerate checked-in artifacts after reviewing `catalog.json`:

```bash
go run ./tools/evalcorpus \
  --catalog ./eval/catalog.json \
  --output ./eval/cases
```

CI also asserts the exact 50-case composition, priority distribution, provenance split, layer coverage, and expected blocked/passed gate counts.

## Metric interpretation

The HTML and JSON reports include precision, recall, F1, P0-P3 recall, gate accuracy, false-block rate, unresolved hypotheses, latency, and token usage.

The checked-in corpus currently reports 100% replay precision/recall because the reports are golden regression artifacts. This proves that schema migration, matching, reporting, Needs Review handling, and merge-gate policy remain stable. It **does not** prove 100% DeepSeek accuracy.

A publishable model-quality claim requires a separate live evaluation with fresh pipeline outputs, repeated probabilistic cases, failure rates, latency, token use, and a defensible uncertainty analysis. Golden replay and live-model metrics must remain separately labeled. Repeated runs on the same small synthetic corpus are correlated and must not be presented as independent samples for a narrow confidence interval.

## Executable live pipeline evaluation

`eval/live/corpus.json` is a separate, independently labeled corpus with **12 executable synthetic Go PRs: 8 bug cases and 4 clean controls**. It contains literal base/head source files and expected findings, not saved model outputs. Cases cover credential leakage into child processes, SQL injection, shell injection, path traversal, cross-file authorization, TLS verification, nil handling, executable regression tests, and clean negative controls.

`aegis eval-live` creates temporary local Git repositories, checks that base tests pass, commits the head patch with fixed Git timestamps, and invokes a **separately built trusted Aegis binary** using the exact base/head commit IDs. Independent expectation labels and case descriptions stay outside the reviewed repository and are not supplied to the model. It snapshots that binary once so a concurrent rebuild cannot change the evaluator, and selects a locally installed Go toolchain matching the reviewer's build version to avoid incompatible compiler export data. A missing matching toolchain produces a preflight error, not a misleading benchmark. The full reviewer performs Diff → static analysis → repository context → optional model → Verifier. The harness evaluates the returned report using the same Publisher gate policy, then matches evidence against the independent labels. Fresh reports, exact commits, Go version, and corpus/binary SHA-256 digests are retained in the output before temporary repositories are removed.

Build once and run a no-API baseline:

```bash
go build -o ./bin/aegis ./cmd/aegis
./bin/aegis eval-live \
  --corpus ./eval/live \
  --aegis-binary ./bin/aegis \
  --agent-provider none \
  --analyzers default \
  --repeats 1 \
  --format html \
  --output ./eval-live-baseline.html
```

This actually executes the review pipeline, but it does **not** call a model. Misses on semantic defects are expected and remain visible. `--strict=false` is the default so an exploratory baseline can be saved; `--strict` makes missed/extra findings, incomplete runs, wrong gates, or unresolved hypotheses fail CI. A canceled total budget returns nonzero even without strict mode. Request `--analyzers all` only when `staticcheck` and `gosec` are installed; unavailable analyzers are counted as incomplete coverage.

Release binaries built with `-trimpath` may not contain the build machine's GOROOT. If the `go` on PATH is a different version, pass `--go-binary /absolute/path/to/the/matching/go` (for example a Go installation managed by your toolchain manager). An explicit path takes precedence and must match the reviewer's build version; the selected path and version are recorded as `go_binary` and `go_toolchain`. Automatic detection checks the harness's GOROOT, an explicitly configured `GOROOT` environment variable, and PATH. It never downloads a compiler.

For a live model run, set the provider key in the process environment, choose a fixed model you can access, and repeat at least three times. This consumes provider API usage:

```bash
./bin/aegis eval-live \
  --corpus ./eval/live \
  --aegis-binary ./bin/aegis \
  --agent-provider deepseek \
  --agent-model "$AEGIS_EVAL_MODEL" \
  --repeats 3 \
  --case-timeout 5m \
  --timeout 2h \
  --format json \
  --output ./eval-live-deepseek.json
```

`AEGIS_EVAL_MODEL` must name a specific model, and `DEEPSEEK_API_KEY` must already be set. For OrcaRouter, use `--agent-provider orcarouter` with `ORCAROUTER_API_KEY` and its explicit model ID. Generic endpoints use `--agent-provider openai-compatible`, `AEGIS_API_KEY`, `--agent-base-url`, and `--agent-allow-custom-endpoint`. No `.env` file is loaded by live evaluation. The harness forwards only the selected model key to the trusted reviewer; repository child tools receive the reviewer's credential-sanitized environment. The report embeds requested/resolved model metadata whenever returned by the reviewer/provider. No checked-in live-model accuracy claim is made without an actual API run.

Compare DeepSeek direct and OrcaRouter with equivalent pinned model IDs, the same binary/corpus digests, analyzer selection, gate thresholds, and repeat count. Keep automatic routing and fallback visible when interpreting differences. A repeat count of one produces **N/A** for gate consistency, not 100%.

For an explicit Verifier ablation, add `--verifier-enabled=false`. The result records `verifier_enabled: false` and forwards `--verify-agent-candidates=false` to the reviewer. This disables deterministic semantic evidence rules and model-candidate verification; unverified model candidates remain hypotheses and are **not** promoted into findings or counted as true positives. The Publisher still applies its normal independent gate policy. This measures the behavior of Aegis without its verification stage; it is not a separate "trust every LLM answer" baseline. An intentionally disabled stage is not itself marked as an execution failure.

### Live metrics and matching contract

- Finding precision/recall and P0–P3 recall show numerator/denominator support counts; zero-support values are JSON `null` and HTML `N/A`.
- A true positive must match priority, category, file, declared head line range, and at least one independently specified evidence phrase. This conservative lexical rubric can miss differently phrased correct findings; inspect mismatches manually before publishing quality claims. Duplicate findings are not extra true positives.
- Needs Review remains separate and does not count as verified defect recall.
- Gate accuracy requires a complete review with the expected gate; a broken review blocked only by fail-closed policy cannot count as a correct bug detection.
- Clean false-block rate uses only clean runs that produced an observed gate; unknown outcomes are excluded from that denominator and counted in the incomplete rate. Model completion rate, repeated gate consistency, observed tokens, and mean/P95 end-to-end case latency are also reported. Case latency includes fixture creation and base tests; it is not model request latency. Model duration is also available inside each fresh review report.
- Case repetitions are correlated. The report intentionally provides no misleading independent-sample confidence interval, monetary cost estimate, or production accuracy claim.

### Execution boundary

Only use **trusted local corpus files**. Fixture Go tests execute code. The harness rejects remote repository URLs, shell-command fields, path traversal, symlink source destinations, hidden configuration files, custom `go.mod` files, and external module dependencies. It creates isolated Git/Go configuration and caches, disables Go dependency downloads and automatic toolchain downloads, forwards no ambient credentials, bounds subprocess output, and enforces per-case/total timeouts. On Linux/macOS, timeout cancellation terminates the review process group.

These controls are **not an OS sandbox** and do not prevent arbitrary trusted fixture Go code from accessing host files or the network. The checked-in fixtures use synthetic data, never invoke their vulnerable command/network/file operations, and run only benign tests. Do not point the harness at downloaded or PR-controlled corpora on a credential-bearing host; execute those in an appropriate isolated runner.

Integration tests build the real Aegis binary and run repeated credential-flow, failing-test, and clean authorization cases without API access:

```bash
go test ./internal/liveeval ./cmd/aegis
```

The original `aegis eval --corpus eval/cases` remains the unchanged 50-case golden replay regression suite.
