# Security policy

Aegis processes untrusted pull-request code and sends selected code to an optional external model. Its findings are evidence to support a review, not a guarantee that a change is safe.

## Reporting a vulnerability

Use GitHub's private vulnerability reporting for this repository when available. Otherwise open an issue requesting a private contact without posting credentials, exploit details, or private code. Do not include API keys in reports or reproductions.

## Execution boundaries

- The GitHub workflow builds the reviewer from a trusted Aegis revision, never the proposed PR head. The target worktree must match the exact reported head SHA.
- Repository tests, compilers and analyzers run through `--sandbox docker` in the supplied workflow. Containers have no network, no model key, no host PID namespace, no Docker socket, dropped capabilities, read-only source and root filesystem, and bounded memory/process/CPU limits. Git metadata is hidden and VCS build stamping is disabled. A disposable build cache is the only writable host mount.
- Docker source must be a disposable, committed Git checkout with no modified, untracked or ignored files. Worktree indirection, index flags hiding changes, and initialized submodules are rejected. This prevents an ignored local `.env` being mounted into the test container. Never commit secrets to the reviewed source: tracked source content is intentionally visible to analysis and may appear in the model input or report.
- Dependencies must be available in a read-only module cache or the trusted analysis image. A separate secret-free dependency-download container can populate that cache. Analyzer errors are visible as incomplete coverage; Docker failure never silently switches to host execution.
- Publication runs in a fresh job. It reconstructs the summary/HTML and gate from bounded JSON, checks the expected base/head SHAs, and checks the latest PR head before updating the bot comment.
- Fork PRs receive no model credential. Their report remains available through workflow artifacts and summaries. A bot PR comment additionally requires write permission.
- `--sandbox host` is a local-development option for code you trust. Environment-variable filtering alone is **not** an OS sandbox: local test code can access the current user's files and processes.

Containers share the host kernel. Use disposable GitHub-hosted Linux runners. Do not run untrusted reviews on a privileged/self-hosted runner that holds production credentials, mounts the Docker socket into a test container, or shares a writable dependency cache with other jobs. Container isolation does not attest the truthfulness of repository-controlled test output.

## Model and data boundaries

- The model can request only bounded, read-only code tools. Candidate suggestions and verification plans are text; they are not executed as shell commands.
- PR title, body, repository guidance, code and tool results are untrusted evidence, including text which tries to override reviewer instructions. Tool enforcement is independent of those instructions.
- Only a dedicated provider credential variable is accepted: `DEEPSEEK_API_KEY`, `ORCAROUTER_API_KEY`, or `AEGIS_API_KEY`. CI disables dotenv loading and does not load target-repository provider settings.
- HTTPS is required by the CLI. Custom endpoints need explicit opt-in. HTTP redirects are rejected so a configured provider cannot redirect credentials to another host.
- Provider errors omit upstream response bodies and API keys. Reports may still contain sensitive **source code** in findings, snippets, PR descriptions and diffs. Keep report visibility aligned with the repository.
- The OrcaRouter preset is optional. Its gateway retention policy does not establish an end-to-end zero-retention guarantee for upstream models. Review each provider's current terms before sending private code.

## Evidence and gate policy

Default verified-finding threshold: P1 (P0/P1 block; P2/P3 remain visible). Default unresolved-hypothesis threshold: P0. Deterministic analysis and Verifier failures block when `--fail-on-incomplete=true`. Optional context/model failures appear as degraded coverage. `--require-agent=true` additionally requires a completed reasoning run for supported source changes.

An unverified candidate does not become a clean result when verification is disabled or interrupted. The publisher preserves it as Needs Review. Matching a diagnostic does not verify every impact claim in a model response; promoted findings retain the diagnostic's supported description and severity bound.

Add the final workflow gate to your repository's required status checks/ruleset. A failed workflow only prevents merging when the repository enforces that check; Aegis cannot configure branch protection implicitly.

## Reports and public hosting

HTML is self-contained, uses template escaping and a restrictive Content Security Policy, and loads no external script or font. Artifact reports remain the default, and public hosting is disabled by default. Having the publisher code does not establish that a site has been deployed: automatic publication requires the implementation on the trusted default branch, GitHub Actions Pages configuration and the repository opt-in variable. Enabling hosting does not activate or replace the Review workflow.

- **Explicit public authorization:** automatic publication requires the repository variable `AEGIS_PUBLIC_REPORTS=true`; manual dispatch requires `confirm-public` for that publication. Only public repositories are accepted. This is consent to expose report content, not authentication for viewers. Code snippets, PR descriptions, vulnerability details and archived JSON evidence may become public. Never enable this path for confidential material. Disabling the switch does not remove already published pages or Git history.
- **Separate trust and permissions:** the publisher reads only completed, GitHub-associated Aegis PR runs. Both legacy and v1 producer workflows must match audited, pinned SHA256 digests; v1 additionally requires a validated publication manifest. Only direct PR workflows are accepted, not unapproved reusable/nested producer chains; see [source restrictions](docs/report-hosting.md). It does not execute target PR code or deploy artifact-supplied HTML/JavaScript. Source preparation is read-only, archive writing receives repository-content permission separately, deployment uses Pages/OIDC permissions, and feedback receives PR-comment permission in its own job. No model credential is needed.
- **Persistent evidence:** validated JSON and original conclusions are appended to the dedicated, marked `aegis-report-history` branch using non-forced Git ref updates. Conflicting contents for an existing identity are rejected. The archive is bounded to 200 records, 64 MiB total and 17 MiB per record; capacity exhaustion fails rather than deleting history. A trusted default-branch renderer rebuilds every page, and report paths bind PR number, full Head SHA, run ID and attempt.
- **Safe feedback:** webpage links are added only after a successful deployment and must match the HTTPS URL returned by the repository's GitHub Pages API, including a configured custom domain. Only the owned `github-actions[bot]` comment is updated. Current Base/Head, open PR state, newer runs/attempts and comment identity are checked again around pagination. GitHub offers no atomic cross-resource compare-and-swap, so this limits rather than eliminates write races.
- **No gate override:** public delivery cannot turn failed or incomplete evidence into a passed review, and it does not create or change merge checks. Missing evidence can produce an explicit diagnostic page. Failed publication preserves existing Artifact/Check feedback rather than inserting a nonexistent successful report link.
- **Whole-site deployment:** this workflow uses the repository's entire Pages site. Do not enable it over an existing documentation site without a separately reviewed hosting plan. Restrict the `github-pages` environment to the default branch; required reviewers are optional and will pause automatic deployments when enabled.

GitHub Pages is not private-report access control. A separate authenticated service or appropriately scoped private storage is required for private/internal repositories. See [report hosting](docs/report-hosting.md) for setup, retention and limitations.

## Validation limits

Golden replay tests validate regression behavior, not live model accuracy. Executable synthetic fixtures test real pipeline behavior but are not representative production benchmarks. A release should include the actual test runs, tool/model versions, unresolved failures and repeated live-model measurements; never advertise the fixture replay score as a model recall guarantee.
