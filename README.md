# AegisCodeAgent

[English](README.md) | [简体中文](README.zh-CN.md) | [日本語](README.ja.md) | [Español](README.es.md)

A Go-focused code-review agent that lives in GitHub pull requests. Aegis combines static analysis, repository context, bounded model reasoning and independent verification, then publishes a clear P0–P3 decision with an HTML evidence report. No web server or desktop app is required.

> **v1 release candidate, in development.** Reusable workflows, optional model providers, isolated analysis and real-code evaluation are implemented. A release tag, live provider quality results and a deployed report site are **not** implied. See [validation and limitations](docs/validation.md).

## What you get

When a PR is opened or updated, Aegis reviews its exact Head against its Base. It updates one bot comment, attaches file/line annotations, uploads a self-contained HTML report and returns an independent **Aegis merge gate** check.

- **Findings first:** P0–P3, affected code, evidence and suggested action.
- **No false clean:** unresolved model hypotheses stay visible as `Needs Review`; incomplete or degraded coverage is identified separately.
- **Configurable policy:** verified P0/P1 findings and unresolved P0 hypotheses block by default. P2/P3 remain visible without blocking by themselves.
- **Optional reasoning:** DeepSeek direct, OrcaRouter preset, or an explicitly configured OpenAI-compatible endpoint. No API key means deterministic mode; `require-agent: true` makes model completion mandatory.
- **Evidence delivery:** HTML/JSON artifacts and PR feedback by default. Public HTTPS hosting is a separate, explicit opt-in—not an automatic publication of source code.

GitHub only enforces the result after a maintainer makes the actual **Aegis merge gate** check required in branch protection/rulesets. Check names may include the caller job prefix.

## Architecture

```text
PR opened / synchronize
        │
        ▼
Resolve trusted reviewer version + exact PR Base/Head
        │
        ▼
Diff → concurrent static analysis → repository context
        │        go test / vet / staticcheck / gosec
        │        AST / types / callers / tests / PR intent / rules
        ▼
Reasoning Agent ⇄ bounded read-only repository tools
        │
        ▼
Verifier → Verified / Needs Review / Rejected
        │
        ▼
Separate trusted Publisher → HTML + annotations + bot comment
        │
        ▼
Independent required check → merge policy
```

Static diagnostics carry analyzer evidence. LLM candidates must pass identity, changed-line, source-snapshot and independent diagnostic/semantic checks before becoming final findings. A Verifier does not prove every semantic claim; unsupported claims remain unresolved. The model never controls the final gate.

| Component | Responsibility | Code |
| --- | --- | --- |
| Diff | Exact revisions, three-dot changes, hunks and renames | `internal/gitdiff` |
| Analysis | Concurrent tools, bounded output, timeouts, Docker execution | `internal/analyzer` |
| Context | Budgeted Go AST/types/symbol relationships, tests, PR intent and guidance | `internal/context` |
| Agent | Provider capabilities, tool loop, structured candidates and request traces | `internal/agent` |
| Verifier | Source-aware evidence correlation and hypothesis lifecycle | `internal/verifier` |
| Publisher | Independent gate calculation and evidence rendering | `internal/githubreport`, `internal/report` |
| Eval | Golden report replay **and** real-code fixture execution | `internal/eval`, `internal/liveeval` |
| Delivery | Reusable PR workflow, release builds, optional report hosting | `.github/workflows`, `scripts` |

### Execution boundary

For self-review, the reviewer is built from trusted PR Base. For external consumers, it is built from the resolved SHA of the reusable Aegis workflow. It is never compiled from the target PR Head.

The analysis job has read-only GitHub permissions. Repository-controlled Go commands execute inside a no-network Docker container with read-only source, bounded resources and no model key. Public dependencies are prefetched by a separate credential-free container before model secrets are injected. A fresh publisher job validates the exact commits and regenerates output from JSON. There is no unsandboxed fallback in CI.

This is a container boundary, not a virtual machine or a guarantee against kernel vulnerabilities. Local `--sandbox host` is for trusted repositories only. See [SECURITY.md](SECURITY.md).

## Install in another repository

Use one reusable workflow; you do **not** copy the Aegis source into the target repository.

Create `.github/workflows/aegis.yml`:

```yaml
name: Aegis review
on:
  pull_request:
    types: [opened, synchronize, reopened, ready_for_review]
permissions:
  actions: read
  contents: read
  pull-requests: write
jobs:
  review:
    uses: Molly166/AegisCodeAgent/.github/workflows/aegis-review.yml@REPLACE_WITH_RELEASE_COMMIT_SHA
    with:
      provider: deepseek
      model: deepseek-v4-flash
      fail-on: p1
      fail-on-needs-review: p0
      require-agent: false
    secrets:
      provider-api-key: ${{ secrets.DEEPSEEK_API_KEY }}
```

`REPLACE_WITH_RELEASE_COMMIT_SHA` is a placeholder: replace it with the full SHA of an audited, published Aegis revision containing this workflow. This documentation does not claim a `v1` tag already exists.

Add the key in **Settings → Secrets and variables → Actions**. To use OrcaRouter, change `provider` to `orcarouter`, select a tested model such as `deepseek/deepseek-v4-flash`, and reference `secrets.ORCAROUTER_API_KEY`. Model availability must be verified with the provider. To avoid external model calls, use `provider: none` and omit secrets.

Fork/Dependabot PRs do not receive model credentials. Their report remains available in Checks/Artifacts even when GitHub denies PR comment writes. Enabling `require-agent` intentionally blocks this degraded mode. Optional context/agent failures do not turn a P2 finding into a blocker; missing mandatory evidence still fails closed.

Full setup, inputs, upgrade bootstrap and dependency restrictions: [GitHub integration](docs/github-action.md). The first migration from an older trusted Base may require explicit maintainer review because Aegis will not silently build the Head reviewer to bypass the boundary.

## Local development

Requirements: Go 1.24+, Git. v1 uses `os.Root` for traversal-resistant repository file access. CI pins one matching Go toolchain for reviewer and analyzers. The Docker sandbox additionally requires Linux Docker and the trusted analysis image.

If your shell pins an older `GOTOOLCHAIN`, select an installed compatible version (CI uses `go1.26.6`) for the build and tests. A trimmed release binary may need `eval-live --go-binary /absolute/path/to/matching/go`; the evaluator never silently downloads a compiler.

```sh
git clone https://github.com/Molly166/AegisCodeAgent.git
cd AegisCodeAgent
go test ./...
go build -trimpath -o /tmp/aegis ./cmd/aegis

# Run against a trusted, clean checkout at the requested Head.
/tmp/aegis review --repo . --base origin/master --head HEAD \
  --agent-provider none --analyzers default \
  --format html --output /tmp/aegis-review.html
```

`default` runs `go test` and `go vet`. `all` also requires `staticcheck` and `gosec`; the CI analysis image includes them. This CLI is the execution engine, not a separately deployed application. `aegis review` produces evidence; `aegis github` applies publisher policy.

Use `.aegis.example.json` as a **non-secret** configuration example and load it explicitly with `--config`. Keys belong in dedicated environment variables; CI never loads target-repository `.env` or provider configuration. Provider endpoint opt-in, model capabilities, request budgets and trace semantics: [providers](docs/providers.md).

## Evaluation: distinguish regression from detection

| Suite | Input | What passing means |
| --- | --- | --- |
| Golden replay: 50 cases | Versioned reports: 27 Bug, 15 Clean, 5 Needs Review, 3 resilience | Matching and merge policy stay consistent with fixtures; **not** live model accuracy |
| Real-code evaluation: 12 cases | 8 buggy and 4 clean synthetic Git Base/Head fixtures | The built reviewer actually executes; misses, false blocks, incomplete runs and usage remain visible |

```sh
# Fast deterministic report/policy regression. Strict by default.
/tmp/aegis eval --corpus eval/cases --format html --output /tmp/aegis-golden.html

# Actual code execution, no model spend. Expected detector misses are reported.
/tmp/aegis eval-live --corpus eval/live --agent-provider none \
  --analyzers default --repeats 1 --format html --output /tmp/aegis-live.html
```

Real-code runs record corpus/binary hashes, toolchain, exact commits, provider/model, reports and metrics with denominators. They are separate from the golden corpus. The lexical finding matcher is not an independent semantic judge, and 12 synthetic cases cannot establish production accuracy.

Provider-backed `eval-live` explicitly requires a model and uses your API quota. Compare the same corpus, model, budgets and analyzer configuration; use repeated runs and the verifier ablation mode described in [eval/README.md](eval/README.md). No dollar cost is invented from token counts.

## Reports, releases and contributing

- [Report hosting](docs/report-hosting.md): default authenticated artifact delivery; optional manually approved public Pages report. Never publish confidential code on a public site.
- [GitHub integration](docs/github-action.md): six-platform release-candidate builds, SHA256 and immutable consumer version pinning. Building artifacts does not automatically publish a release.
- [Contributing](CONTRIBUTING.md): tests, corpus updates, negative security cases and review workflow.
- [Security](SECURITY.md): supported boundary, secrets, prompt injection, dependency and sandbox limitations.
- [Validation](docs/validation.md): what has actually been run and what still requires external acceptance.

Aegis currently focuses on Go single-module repositories. Other-language diffs and configuration files may supply reasoning context, but do not have equivalent static or semantic verification coverage. Private dependency provisioning, universal semantic proofs, automatic bug fixes and production-wide accuracy claims are outside this release candidate.

## License

[MIT](LICENSE).
