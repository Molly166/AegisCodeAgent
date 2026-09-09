# AegisCodeAgent

[English](README.md) | [简体中文](README.zh-CN.md) | [日本語](README.ja.md) | [Español](README.es.md)

A Go-focused code-review agent that lives in GitHub pull requests. Aegis combines static analysis, repository context, bounded model reasoning and independent verification, then publishes a clear P0–P3 decision with an HTML evidence report. No web server or desktop app is required.

> **v1 release candidate, in development.** Reusable workflows, optional model providers, isolated analysis and real-code evaluation are implemented. A release tag, live provider quality results and a deployed report site are **not** implied. See [validation and limitations](docs/validation.md).

> **Two-stage migration:** while `examples/aegis-review-v1-migration.yml` exists, this revision is stage 1 and retains the old active PR workflow. The four-job sandboxed/reusable workflow described below is **not yet active**. Stage 2 requires removing that file and activating v1 in `.github/workflows/aegis-review.yml`, only after the implementation has landed on trusted `master`. PR #12 exposed upgrade and CI failures; fixes still need online acceptance. Follow the [ordered migration plan](docs/github-action.md), never trust the target Head as a shortcut. Configuration activation alone does not prove a successful deployment.

## What you get

With the v1 workflow activated and validated, opening or updating a PR makes Aegis review its exact Head against its Base. It updates one bot comment, attaches file/line annotations, uploads a self-contained HTML report and returns an independent **Aegis merge gate** check.

- **Findings first:** P0–P3, affected code, evidence and suggested action.
- **No false clean:** unresolved model hypotheses stay visible as `Needs Review`; incomplete or degraded coverage is identified separately.
- **Configurable policy:** verified P0/P1 findings and unresolved P0 hypotheses block by default. P2/P3 remain visible without blocking by themselves.
- **Optional reasoning:** DeepSeek direct, OrcaRouter preset, or an explicitly configured OpenAI-compatible endpoint. No API key means deterministic mode; `require-agent: true` makes model completion mandatory.
- **Evidence delivery:** HTML/JSON artifacts and PR feedback by default. Once a maintainer explicitly enables public hosting, completed reviews can automatically publish versioned HTTPS reports and update the same PR comment; hosting is disabled by default.

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

After stage 2 passes acceptance, use one reusable workflow; you do **not** copy the Aegis source into the target repository. A stage-1 revision still has the old workflow and is not a compatible `workflow_call` installation target.

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

### Open reports in the browser

Public hosting is **disabled by default**; having the code does not mean a site is deployed. To enable automatic publishing, merge the publisher into the default branch, select **GitHub Actions** in Pages settings and set the repository variable `AEGIS_PUBLIC_REPORTS=true`. An optional `github-pages` environment approval can still pause each deployment. The publisher does not activate or replace the Review workflow, and v1 activation is not a prerequisite.

Both legacy and v1 report-producer workflows must match an audited, pinned SHA256; v1 also requires a validated publication manifest. Public hosting currently accepts direct PR workflows only, not unapproved reusable/nested producer chains. See [supported sources and setup](docs/report-hosting.md).

After a completed review and successful deployment, the same PR bot comment receives a prominent webpage link. Reports have separate `reports/pr-<number>/<full-head-sha>/<run-id>-<attempt>/index.html` paths; validated JSON history is appended to `aegis-report-history` and every deployment rebuilds the full archive. Limits are 200 records / 64 MiB total / 17 MiB per record, with explicit failure instead of automatic deletion. Publication failures leave existing Artifact/Check feedback intact and do not change the merge gate.

**Public repositories only.** Both code/vulnerability details and archived JSON become public. This uses the repository's entire Pages site: do not enable it over an existing documentation site without a separate deployment plan. Disabling the variable does not remove previously published evidence. A manual dispatch with `confirm-public` can authorize a single publication without enabling automatic publishing. Use the HTTPS URL returned after a successful deployment; [setup, permissions and recovery](docs/report-hosting.md).

- [Report hosting](docs/report-hosting.md): opt-in automatic HTTPS history and manual recovery; Artifact delivery remains the default. Never publish confidential code on a public site.
- [GitHub integration](docs/github-action.md): six-platform release-candidate builds, SHA256 and immutable consumer version pinning. Building artifacts does not automatically publish a release.
- [Contributing](CONTRIBUTING.md): tests, corpus updates, negative security cases and review workflow.
- [Security](SECURITY.md): supported boundary, secrets, prompt injection, dependency and sandbox limitations.
- [Validation](docs/validation.md): what has actually been run and what still requires external acceptance.

Aegis currently focuses on Go single-module repositories. Other-language diffs and configuration files may supply reasoning context, but do not have equivalent static or semantic verification coverage. Private dependency provisioning, universal semantic proofs, automatic bug fixes and production-wide accuracy claims are outside this release candidate.

## License

[MIT](LICENSE).
