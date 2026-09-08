#!/usr/bin/env bash
# Keep local and CI lint coverage identical; actionlint otherwise silently skips
# embedded shell checks when ShellCheck is not installed.
set -euo pipefail

workflow_shellcheck=$(command -v shellcheck) || {
  printf 'ShellCheck is required for complete workflow validation; install it and retry.\n' >&2
  exit 2
}
command -v go >/dev/null || {
  printf 'Go is required to run the pinned actionlint version.\n' >&2
  exit 2
}
workflow_root=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
cd -- "$workflow_root"
shopt -s nullglob
workflow_files=(.github/workflows/*.yml .github/workflows/*.yaml)
if [[ ${#workflow_files[@]} -eq 0 ]]; then
  printf 'No active GitHub workflows found.\n' >&2
  exit 2
fi
# This staged workflow is linted before activation. Once migrated, only the
# active copy remains and is already covered by the workflow glob above.
if [[ -f examples/aegis-review-v1-migration.yml ]]; then
  workflow_files+=(examples/aegis-review-v1-migration.yml)
fi
go run github.com/rhysd/actionlint/cmd/actionlint@v1.7.12 \
  -shellcheck "$workflow_shellcheck" "${workflow_files[@]}"

workflow_scripts=(scripts/*.sh)
"$workflow_shellcheck" --shell=bash "${workflow_scripts[@]}"
