# AegisCodeAgent

[![CI](https://github.com/Molly166/AegisCodeAgent/actions/workflows/ci.yml/badge.svg?branch=master)](https://github.com/Molly166/AegisCodeAgent/actions/workflows/ci.yml)
[![Aegis Code Review](https://github.com/Molly166/AegisCodeAgent/actions/workflows/aegis-review.yml/badge.svg?branch=master)](https://github.com/Molly166/AegisCodeAgent/actions/workflows/aegis-review.yml)
![Go 1.23+](https://img.shields.io/badge/Go-1.23%2B-00ADD8?logo=go&logoColor=white)
[![License: MIT](https://img.shields.io/badge/License-MIT-green.svg)](LICENSE)

[English](README.md) | [简体中文](README.zh-CN.md) | [日本語](README.ja.md) | [Español](README.es.md)

AegisCodeAgent is a Go-native code-review agent that runs automatically on GitHub pull requests. It combines deterministic analysis, repository-level context, LLM reasoning, and independent verification so that only evidence-bearing findings reach the final review.

> **Current milestone: v0.7.** Aegis now includes a replay Eval Harness, semantic verification, explicit Needs Review gating, PR-intent context, and a four-analyzer GitHub pipeline. It remains Go-focused and uses DeepSeek as its first reasoning provider.

## What happens when you open a pull request?

You do not start a server or keep a local process running. GitHub Actions starts Aegis, reviews the exact PR head against its base, and publishes the result back to the pull request.

```text
Pull request opened or updated
          │
          ▼
  trusted workflow orchestration
          │
          ▼
Git diff and changed-line scope
          │
          ├──────────────► deterministic Go analyzers
          │                 go test · go vet · staticcheck · gosec
          │
          └──────────────► repository context engine
                            PR intent · rules · AST · types · callers · tests
                                      │
                                      ▼
                           DeepSeek reasoning agent
                                      │
                                      ▼
                             evidence verifier
                                      │
                                      ▼
                 P0-P3 annotations · Job Summary · HTML report
```

The model does not directly decide the merge result. Static findings already carry reproducible evidence. Model-generated candidates must pass location, diff, source-snapshot, focused diagnostics, or semantic evidence checks before promotion. Invalid hypotheses are rejected; unresolved hypotheses become visible `Needs Review` items and P0 items block by default instead of being reported as a clean review.

## Architecture

The system is split into stages with explicit inputs and outputs:

| Stage | Responsibility | Output | Implementation |
| --- | --- | --- | --- |
| GitHub orchestration | React to PR events, check out trusted base and exact head commits, cancel stale runs | Reproducible review workspace | `.github/workflows/aegis-review.yml` |
| Diff engine | Resolve revisions safely and parse three-dot Git diffs, renames, binary files, hunks, and changed lines | Normalized change set | `internal/gitdiff/` |
| Static analysis | Run analyzers concurrently with timeouts, normalize and deduplicate diagnostics | Evidence-backed findings | `internal/analyzer/` |
| Context engine | Combine PR title/body/labels/issues and repository guidance with ranked Go declarations and type relationships | Repository context bundle | `internal/context/` |
| Reasoning agent | Let DeepSeek inspect bounded evidence through read-only tools and propose structured candidates | Unverified candidates | `internal/agent/` |
| Verifier V2 | Validate identity/location, rerun focused checks, execute source-aware semantic rules, and correlate independent evidence | Verified/needs-review/rejected verdicts | `internal/verifier/` |
| Publisher | Map severity to P0-P3, apply the merge threshold, render GitHub output and full reports | Summary, annotations, HTML/JSON | `internal/githubreport/`, `internal/report/` |
| Eval Harness | Replay versioned buggy/clean reports, match expectations, and score findings plus gate behavior | Precision, recall, F1, P0/P1 recall, gate accuracy, false-block rate | `internal/eval/`, `eval/cases/` |
| Credential boundary | Remove credential-shaped variables from repository-controlled subprocesses | Sanitized child environment | `internal/secureenv/` |

### Finding lifecycle

```text
deterministic diagnostic ──────────────────────────────► final finding

model candidate
      │
      ▼
schema + repository boundary + changed-line validation
      │
      ▼
focused analyzer + semantic evidence correlation
      │
      ├── verified ────────────────────────────────────► final finding
      ├── rejected ────────────────────────────────────► audit trail only
      └── needs review ────────────────────────────────► visible human-review queue; P0 blocks by default
```

The final report is a stable `ReviewReport` domain model shared by the CLI, GitHub publisher, HTML renderer, and JSON automation interface. This keeps review logic independent from presentation.

## Quick start: automatic GitHub review

This is the recommended way to use Aegis in this repository or in your own fork.

### 1. Prepare the repository

Fork the repository if necessary, then clone it:

```bash
git clone https://github.com/<YOUR_GITHUB_NAME>/AegisCodeAgent.git
cd AegisCodeAgent
go test ./...
```

Make sure GitHub Actions is enabled under **Settings → Actions → General**.

### 2. Choose the review mode

No secret is required for the deterministic mode:

```text
go test + go vet + staticcheck + gosec + semantic verifier + GitHub report
```

For the full reasoning and verification path, create a repository secret under **Settings → Secrets and variables → Actions**:

```text
Name:  DEEPSEEK_API_KEY
Value: <your DeepSeek API key>
```

Fork and Dependabot pull requests never receive this secret and automatically use deterministic mode.

### 3. Push a normal feature branch

```bash
git switch master
git pull --ff-only origin master
git switch -c feature/my-change

# Edit code or documentation.
git add .
git commit -m "feat: describe the change"
git push -u origin feature/my-change
```

Open a pull request from `feature/my-change` to `master`. The `opened` event starts Aegis; every later push emits a `synchronize` event, starts a fresh review, and cancels the stale run.

Documentation-only pull requests still trigger the workflow and receive a summary and report artifact. Aegis may skip unnecessary model reasoning when there is no supported source-code evidence, while deterministic analysis, report publication, and merge-gate semantics remain auditable.

### 4. Read the result

Open the pull request and inspect:

1. **Conversation** for Aegis's continuously updated review comment and complete HTML report link.
2. **Checks → Aegis Code Review** for execution state and the Job Summary.
3. **Annotations** for findings attached to changed files and lines.
4. **Artifacts → aegis-review-report** for `review.html` and `review.json`.
5. The final check conclusion for the merge decision.

| Priority | Meaning | GitHub annotation | Blocks by default |
| --- | --- | --- | :---: |
| P0 | Critical | Error | Yes |
| P1 | High | Error | Yes |
| P2 | Medium | Warning | No |
| P3 | Low / Info | Notice | No |

To enforce the result, add `Aegis Code Review` as a required status check in the `master` branch ruleset. GitHub's own notification settings provide web and email notifications; Aegis does not run a separate mail service.

> Aegis is currently repository-native rather than a Marketplace action. It works out of the box in this repository and its forks. Installing it into an unrelated repository currently requires bringing the Aegis source and workflow into that repository; packaging it as a reusable action is future work.

## Quick start: local CLI

Requirements: Go 1.23+ and Git.

### Deterministic review without an API key

```bash
go build -o aegis ./cmd/aegis

./aegis review \
  --repo . \
  --base master \
  --head HEAD \
  --output review.html
```

Open `review.html` in a browser. The checked-out worktree must be clean and match `HEAD`, because analyzers operate on the real filesystem.

### Full DeepSeek + Verifier review

```bash
cp .aegis.example.json .aegis.json
cp .env.example .env

# Put the real key only in the ignored .env file.
./aegis review \
  --config .aegis.json \
  --repo . \
  --base master \
  --head HEAD \
  --output review.html
```

`.aegis.json` stores non-secret provider settings, budgets, and timeouts. The API key is read only from `DEEPSEEK_API_KEY` or the ignored `.env`. The official DeepSeek endpoint is enforced unless a custom endpoint is explicitly allowed.

Useful commands:

```bash
# Machine-readable report
./aegis review --repo . --base master --head HEAD --format json --output review.json

# Run every adapter when staticcheck and gosec are installed
./aegis review --repo . --base master --head HEAD --analyzers all --output review.html

# Replay the checked-in regression corpus and generate a self-contained dashboard
./aegis eval --corpus ./eval/cases --format html --output eval-report.html

# Discover every available option
./aegis review --help
./aegis github --help
./aegis eval --help
```

## Security model

- The review binary is built from the trusted PR base commit, while the exact head commit is checked out separately as the analysis target.
- The workflow uses read-only repository permission and disables persisted checkout credentials.
- Fork and Dependabot PRs receive neither `DEEPSEEK_API_KEY` nor write-capable tokens.
- Git, tests, vet, staticcheck, and gosec run with credential-shaped environment variables removed.
- Model tools are read-only, path-confined, line-bounded, and output-bounded.
- Same-repository branches are trusted by GitHub for secret access. Restrict write access and require review for changes under `.github/workflows/`.

## Development guide

Package boundaries:

```text
cmd/aegis/             CLI orchestration and user-facing errors
internal/gitdiff/      revision resolution and unified-diff parsing
internal/analyzer/     analyzer adapters, scheduling, normalization
internal/context/      AST/type index, relationships, ranking, budgets
internal/config/       strict JSON config and narrow dotenv loading
internal/agent/        provider protocol, prompt, tools, reasoning loop
internal/verifier/     candidate validation and evidence adjudication
internal/eval/         replay corpus, matching, quality metrics, HTML dashboard
internal/githubreport/ P0-P3 mapping, Summary, annotations, merge gate
internal/secureenv/    child-process credential isolation
internal/review/       shared domain model
internal/report/       self-contained HTML, JSON, and Markdown renderers
```

Run the quality gates before opening a pull request:

```bash
go fmt ./...
go vet ./...
go test -race ./...
go build ./cmd/aegis
```

Every new finding source must provide a real location, severity, category, source, confidence, and reproducible evidence. New Agent candidates must remain separate from final findings until verification completes.

## Current scope and roadmap

Completed:

- deterministic Go analyzer pipeline;
- repository context engine;
- bounded DeepSeek reasoning loop;
- semantic Verifier V2 with explicit Needs Review outcomes;
- replay Eval Harness and seed buggy/clean regression corpus;
- PR-intent and repository-guidance context;
- full go test, go vet, staticcheck, and gosec workflow execution;
- self-contained HTML evidence report;
- GitHub Actions trigger, annotations, artifact, and merge gate.

Next:

- grow the seed corpus into a statistically useful, independently labeled benchmark;
- add live-model comparison, repeated trials, and confidence intervals;
- reusable GitHub Action packaging and release distribution;
- additional model providers and production observability.

## License

[MIT](LICENSE)
