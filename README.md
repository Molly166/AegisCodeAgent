# AegisCodeAgent

AegisCodeAgent is a Go-native, evidence-driven agent for pull-request code review. It is being built as an engineering system: deterministic analysis establishes facts, repository context explains impact, an LLM reasons over that evidence, and a verifier filters unsupported findings before publication.

The project is currently in **Phase 5: verification pipeline**. The CLI resolves Git revisions safely, runs deterministic Go analyzers, builds a budgeted repository context bundle, runs a bounded DeepSeek reasoning loop, and adjudicates every Agent candidate through an independent local evidence gate. Only candidates corroborated by a focused deterministic diagnostic can enter the final findings and affect the verdict.

## What works today

- Compare two Git revisions using three-dot (`merge-base...head`) semantics.
- Parse modified, added, deleted, renamed, binary, quoted, and Unicode paths.
- Preserve hunk context and old/new line coordinates for downstream analyzers.
- Run `go test` and `go vet` by default against affected Go packages.
- Optionally adapt `staticcheck` and `gosec` machine-readable output.
- Run analyzers concurrently with independent timeouts and bounded output capture.
- Filter static diagnostics to added lines, deduplicate them, rank severity, and assign stable fingerprints.
- Report analyzer status as passed, findings, skipped, unavailable, failed, or timed out.
- Discover repository packages with `go list` and index declarations with Go AST.
- Use best-effort `go/types` information for exact call and interface implementation edges.
- Fall back to confidence-labelled syntax inference when full type information is unavailable.
- Locate changed functions, methods, types, interfaces, variables, constants, tests, and file-level changes.
- Rank direct callers, callees, receiver types, related interfaces, and relevant tests.
- Enforce symbol, snippet, file-size, and total-context budgets with transparent truncation statistics.
- Call DeepSeek through a provider interface using the current Chat Completions tool-call protocol.
- Preserve thinking-mode context across tool turns without storing or publishing private reasoning content.
- Let the model inspect only bounded line ranges and plain-text search results through read-only tools.
- Require JSON output, validate every candidate locally, reject non-added-line locations, and assign stable fingerprints.
- Keep Agent candidates separate from deterministic findings and record steps, tools, latency, token usage, and warnings.
- Load non-secret Agent settings from an explicit JSON config and the API key from the environment or ignored `.env` only.
- Recheck candidate identity, changed-line membership, repository boundaries, symlinks, and exact source snapshots.
- Rerun focused `go test` and `go vet` for packages containing structurally valid Go candidates.
- Correlate diagnostics by overlapping location, compatible category, and shared defect signals instead of title-only matching.
- Adjudicate every candidate as `verified`, `rejected`, or `inconclusive`; never treat a passing test suite as proof that a hypothesis is false.
- Link candidates already covered by static findings without duplication, and promote only newly corroborated candidates.
- Calibrate confidence and cap promoted severity at the strongest deterministic evidence level.
- Produce a responsive, printable, dependency-free HTML report.
- Export Markdown for compatibility and versioned JSON for automation.
- Run unit and Git integration tests in GitHub Actions.

## Quick start

Requirements: Go 1.23+ and Git.

```bash
go build -o aegis ./cmd/aegis
./aegis review --repo . --base main --head HEAD --output review.html
```

Open `review.html` in a browser. HTML is the default report format, so `--format html` is optional. By default, analysis runs `go test` and `go vet` for changed Go packages.

### Enable the DeepSeek reasoning agent

Remote model use is explicit. Copy the safe examples, put the real API key only in the ignored `.env`, and pass the config path on the command line:

```bash
cp .aegis.example.json .aegis.json
cp .env.example .env
# Edit .env and replace the placeholder with your real DEEPSEEK_API_KEY.

./aegis review \
  --config .aegis.json \
  --repo . \
  --base main \
  --head HEAD \
  --output review.html
```

The default model is `deepseek-v4-flash`; change `model` to `deepseek-v4-pro` in `.aegis.json` for a quality-first run. The config contains the model, official Base URL, thinking mode, effort, Agent budgets, and Verifier timeouts. It contains only the name `DEEPSEEK_API_KEY`, never the key itself.

The official DeepSeek endpoint is enforced by default. Sending code and credentials to a compatible proxy requires both a custom `--agent-base-url` and the explicit `--agent-allow-custom-endpoint` flag.

Repository context is enabled by default and indexes all repository packages so cross-package callers can be discovered. Limit it to affected packages when reviewing a very large monorepo:

```bash
./aegis review \
  --repo . \
  --base main \
  --head HEAD \
  --context-scope changed \
  --context-max-symbols 40 \
  --context-max-bytes 49152 \
  --output review.html
```

Run every supported analyzer when `staticcheck` and `gosec` are installed on `PATH`:

```bash
./aegis review \
  --repo . \
  --base main \
  --head HEAD \
  --analyzers all \
  --output review.html
```

For evidence integrity, the checked-out worktree must be clean and match the requested head commit. `--allow-dirty-analysis` is available for explicit local experimentation, but its results may not correspond exactly to the Git comparison.

For CI or other tools:

```bash
./aegis review --repo . --base main --head HEAD --format json --output review.json
```

All review flags:

```text
--repo       path to the Git repository (default ".")
--config     explicit path to an Aegis JSON config file
--base       base Git revision (default "main")
--head       head Git revision (default "HEAD")
--format     html, markdown, or json (default "html")
--output     output path, or - for stdout (default "-")
--context    context lines per diff hunk (default 3)
--timeout    maximum time for Git operations (default 30s)
--analyzers  default, all, none, or a comma-separated analyzer list
--analysis-scope  changed or all (default "changed")
--changed-lines-only  publish static diagnostics only on added lines (default true)
--analyzer-timeout  maximum time for each analyzer (default 2m)
--allow-dirty-analysis  explicitly permit analysis outside the requested clean head
--repo-context  build repository context for changed Go symbols (default true)
--context-scope  changed or all (default "all")
--context-max-symbols  maximum selected context symbols (default 40)
--context-max-bytes  approximate context payload budget (default 49152)
--context-timeout  repository indexing deadline (default 1m)
--agent-provider  none or deepseek (default "none" unless config enables it)
--agent-model  model name (default "deepseek-v4-flash")
--agent-base-url  provider API base URL (default official DeepSeek endpoint)
--agent-api-key-env  credential variable name (DeepSeek requires DEEPSEEK_API_KEY)
--agent-thinking  enable thinking mode (default true)
--agent-reasoning-effort  low, medium, high, xhigh, or max (default "high")
--agent-timeout  complete loop deadline (default 3m)
--agent-max-steps  maximum model turns (default 6)
--agent-max-candidates  maximum accepted candidates (default 12)
--agent-max-input-bytes  serialized evidence budget (default 98304)
--agent-max-output-tokens  output limit per turn (default 8192)
--agent-allow-custom-endpoint  explicit credential/code egress opt-in
--verify-agent-candidates  run the deterministic evidence gate (default true)
--verifier-timeout  complete verification deadline (default 3m)
--verifier-analyzer-timeout  per-tool focused analyzer deadline (default 2m)
```

## Architecture

```text
Git revisions
    │
    ▼
Diff collector ──► unified-diff parser ──► affected package selector
                                                │
                         ┌──────────────────────┴──────────────────────┐
                         ▼                                             ▼
             deterministic analyzer pipeline              AST + go/types context
          go test · vet · staticcheck · gosec       callers · callees · interfaces · tests
                         │                                             │
                         └──────────────────────┬──────────────────────┘
                                                ▼
                              filter · dedupe · rank · budget
                                                │
                                                ▼
                                  bounded reasoning loop
                            DeepSeek · read lines · search code
                                                │
                                                ▼
                              unverified candidate findings
                                                │
                                                ▼
                                   deterministic verifier
                         identity · diff · snapshot · test · vet
                                                │
                         ┌──────────────────────┼──────────────────────┐
                         ▼                      ▼                      ▼
                      verified              rejected             inconclusive
                         │
                         ▼
                    final findings
                         │
                         ┌──────────────────────┼──────────────────────┐
                         ▼                      ▼                      ▼
                    HTML dossier          JSON contract         Markdown report

Next phases: GitHub PR ──► evaluation ──► production hardening
```

Current package boundaries:

```text
cmd/aegis/          CLI entry point and user-facing errors
internal/gitdiff/   Git execution, revision resolution, diff parsing
internal/analyzer/  Tool execution, package scope, analyzers, aggregation
internal/context/   AST/type index, relationship graph, ranking, budgets
internal/config/    Strict JSON config and narrow dotenv credential loading
internal/agent/     Provider protocol, DeepSeek adapter, prompt, tools, loop, validation
internal/verifier/  Local invariants, focused analyzers, evidence correlation, adjudication
internal/review/    Stable domain model shared by every review stage
internal/report/    Self-contained HTML, JSON, and Markdown renderers
```

## Roadmap to the full agent

1. ✅ **Deterministic analyzer layer** — adapters for `go test`, `go vet`, `staticcheck`, and `gosec`; normalize output into evidence-bearing findings.
2. ✅ **Repository context engine** — retrieve changed symbols, interfaces, callers, implementations, and tests under strict budgets; exact type resolution is used when available and confidence-labelled AST inference is the fallback.
3. ✅ **Reasoning loop** — plan bounded read-only tool calls, preserve thinking tool turns, produce locally validated candidate findings, and isolate the DeepSeek provider behind an extensible interface.
4. ✅ **Verification pipeline** — validate candidate integrity and exact locations, rerun focused tests/vet, correlate independent diagnostics, calibrate confidence, deduplicate existing evidence, and withhold unsupported findings.
5. **GitHub integration** — GitHub App/Action, check runs, inline PR comments, idempotent updates, permissions hardening, and prompt-injection isolation.
6. **Evaluation** — curated buggy/clean PR corpus, precision and recall, false-positive rate, latency/cost percentiles, and ablation experiments.

## Quality gates

```bash
go fmt ./...
go vet ./...
go test -race ./...
go build ./cmd/aegis
```

Every finding added by a future analyzer must include a file location, severity, category, source, confidence, and reproducible evidence. That contract is the basis for keeping the final agent measurable rather than subjective.
