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

HTML is self-contained, uses template escaping and a restrictive Content Security Policy, and loads no external script or font. Artifact reports are the default. Optional public report hosting must be explicitly enabled for public repositories; review the source report before publication. Private code must not be copied to public GitHub Pages. See [report hosting](docs/report-hosting.md).

## Validation limits

Golden replay tests validate regression behavior, not live model accuracy. Executable synthetic fixtures test real pipeline behavior but are not representative production benchmarks. A release should include the actual test runs, tool/model versions, unresolved failures and repeated live-model measurements; never advertise the fixture replay score as a model recall guarantee.
