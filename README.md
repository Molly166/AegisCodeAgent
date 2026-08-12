# AegisCodeAgent

[English](README.md) | [简体中文](README.zh-CN.md)

AegisCodeAgent is a Go-native, evidence-driven agent for pull-request code review. It is being built as an engineering system: deterministic analysis establishes facts, repository context explains impact, an LLM reasons over that evidence, and a verifier filters unsupported findings before publication.

The project is currently in **Phase 6: GitHub Actions review integration**. Pull-request updates now trigger a trusted Aegis reviewer automatically. The engine resolves Git revisions safely, runs deterministic Go analyzers, builds a budgeted repository context bundle, executes a bounded DeepSeek reasoning loop when a same-repository secret is available, and adjudicates every Agent candidate through an independent local evidence gate.

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
- Review pull requests automatically when they are opened, reopened, marked ready, or receive a new commit.
- Publish P0-P3 GitHub annotations and a native Job Summary; fail the merge gate on P0/P1 by default.
- Build the reviewer from the trusted PR base commit while analyzing the exact head commit in a separate worktree.
- Strip credential-shaped environment variables from every repository-controlled Git and analyzer subprocess.
- Upload the complete HTML and JSON evidence as a GitHub Actions artifact and cancel stale runs after a new push.
- Run unit and Git integration tests in GitHub Actions.

## Automatic GitHub pull-request review

No local Aegis process is required. The repository includes [`.github/workflows/aegis-review.yml`](.github/workflows/aegis-review.yml). Once this workflow exists on `master`, opening a pull request or pushing a new commit to it automatically starts the `Aegis Code Review` check.

To enable the full DeepSeek + Verifier path, add the key under **Settings → Secrets and variables → Actions → New repository secret**:

```text
DEEPSEEK_API_KEY=<your key>
```

Without that secret, Aegis still runs deterministic `go test` and `go vet` review. Fork and Dependabot pull requests never receive the secret and automatically use this static-only mode.

Each run publishes:

- a GitHub Job Summary with P0/P1/P2/P3 counts and stage status;
- line-level `error`, `warning`, and `notice` annotations in the pull request checks;
- an `aegis-review-report` artifact containing `review.html` and `review.json`;
- a failed `Aegis Code Review` check when P0/P1 exists or a requested review stage is incomplete.

Priority mapping is deterministic: `critical → P0`, `high → P1`, `medium → P2`, and `low/info → P3`. Add `Aegis Code Review` as a required status check in the `master` branch ruleset if P0/P1 findings must block merges. GitHub's existing notification settings handle web and email notifications for failed checks; Aegis does not operate a separate mail service.

Security boundary: fork and Dependabot pull requests are untrusted and always run without repository secrets. Same-repository branches are treated as trusted by GitHub for secret access, so restrict write access and require review for changes under `.github/workflows/`. Aegis still builds the review engine from the base commit and strips credential-shaped variables from repository-controlled subprocesses as defense in depth.

Bootstrap note: the pull request that first introduces this workflow is reviewed by the older trusted binary from `master` in static-only mode. Its proposed binary is used only to publish the already-produced JSON and receives no secret. Manually inspect that first run and the workflow diff; after it is merged, later pull requests use the complete trusted v0.6+ pipeline from the base commit.

## Local CLI (optional)

Requirements: Go 1.23+ and Git.

```bash
go build -o aegis ./cmd/aegis
./aegis review --repo . --base master --head HEAD --output review.html
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
  --base master \
  --head HEAD \
  --output review.html
```

The default model is `deepseek-v4-flash`; change `model` to `deepseek-v4-pro` in `.aegis.json` for a quality-first run. The config contains the model, official Base URL, thinking mode, effort, Agent budgets, and Verifier timeouts. It contains only the name `DEEPSEEK_API_KEY`, never the key itself.

The official DeepSeek endpoint is enforced by default. Sending code and credentials to a compatible proxy requires both a custom `--agent-base-url` and the explicit `--agent-allow-custom-endpoint` flag.

Repository context is enabled by default and indexes all repository packages so cross-package callers can be discovered. Limit it to affected packages when reviewing a very large monorepo:

```bash
./aegis review \
  --repo . \
  --base master \
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
  --base master \
  --head HEAD \
  --analyzers all \
  --output review.html
```

For evidence integrity, the checked-out worktree must be clean and match the requested head commit. `--allow-dirty-analysis` is available for explicit local experimentation, but its results may not correspond exactly to the Git comparison.

For CI or other tools:

```bash
./aegis review --repo . --base master --head HEAD --format json --output review.json
```

All review flags:

```text
--repo       path to the Git repository (default ".")
--config     explicit path to an Aegis JSON config file
--base       base Git revision (default "master")
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

The Workflow uses the internal publisher as follows:

```text
aegis github
  --report review.json
  --html-output review.html
  --summary "$GITHUB_STEP_SUMMARY"
  --fail-on p1
  --fail-on-incomplete=true
  --max-annotations 50
```

`--fail-on` accepts `p0`, `p1`, `p2`, `p3`, or `none`. This command is intended for GitHub Actions; regular users normally do not invoke it directly.

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

Next phases: evaluation ──► production hardening
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
internal/githubreport/ P0-P3 mapping, GitHub Summary, annotations, merge gate
internal/secureenv/    Credential stripping for repository-controlled child processes
internal/review/    Stable domain model shared by every review stage
internal/report/    Self-contained HTML, JSON, and Markdown renderers
```

## Roadmap to the full agent

1. ✅ **Deterministic analyzer layer** — adapters for `go test`, `go vet`, `staticcheck`, and `gosec`; normalize output into evidence-bearing findings.
2. ✅ **Repository context engine** — retrieve changed symbols, interfaces, callers, implementations, and tests under strict budgets; exact type resolution is used when available and confidence-labelled AST inference is the fallback.
3. ✅ **Reasoning loop** — plan bounded read-only tool calls, preserve thinking tool turns, produce locally validated candidate findings, and isolate the DeepSeek provider behind an extensible interface.
4. ✅ **Verification pipeline** — validate candidate integrity and exact locations, rerun focused tests/vet, correlate independent diagnostics, calibrate confidence, deduplicate existing evidence, and withhold unsupported findings.
5. ✅ **GitHub Actions integration** — run reviews on pull-request updates, publish check summaries and inline annotations, upload HTML artifacts, support idempotent reruns, and isolate untrusted contributions from secrets.
6. **Evaluation** — curated buggy/clean PR corpus, precision and recall, false-positive rate, latency/cost percentiles, and ablation experiments.

## Quality gates

```bash
go fmt ./...
go vet ./...
go test -race ./...
go build ./cmd/aegis
```

Every finding added by a future analyzer must include a file location, severity, category, source, confidence, and reproducible evidence. That contract is the basis for keeping the final agent measurable rather than subjective.
