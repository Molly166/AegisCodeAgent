#!/usr/bin/env bash
set -euo pipefail

[[ "${AEGIS_BASE:?}" =~ ^[0-9a-f]{40}$ && "${AEGIS_HEAD:?}" =~ ^[0-9a-f]{40}$ ]]
case "${AEGIS_PROVIDER:?}" in
  none|deepseek|openai-compatible|orcarouter) ;;
  *) printf 'Unsupported provider\n' >&2; exit 2 ;;
esac

provider=$AEGIS_PROVIDER
key_env=AEGIS_API_KEY
if [[ "$provider" != none && -z "${AEGIS_PROVIDER_API_KEY:-}" ]]; then
  provider=none
  printf '::notice title=Aegis static-only review::No permitted API key is available. Reasoning is disabled; static analysis, context and merge gates remain active.\n'
fi
case "$provider" in
  deepseek) export DEEPSEEK_API_KEY="$AEGIS_PROVIDER_API_KEY"; key_env=DEEPSEEK_API_KEY ;;
  orcarouter) export ORCAROUTER_API_KEY="$AEGIS_PROVIDER_API_KEY"; key_env=ORCAROUTER_API_KEY ;;
  openai-compatible) export AEGIS_API_KEY="$AEGIS_PROVIDER_API_KEY" ;;
esac

args=(
  --config '' --agent-no-dotenv
  --repo "$GITHUB_WORKSPACE/target" --base "$AEGIS_BASE" --head "$AEGIS_HEAD"
  --analyzers all --sandbox docker --sandbox-image aegis-analysis:local
  --sandbox-modcache "$RUNNER_TEMP/aegis-modcache"
  --agent-provider "$provider" --agent-api-key-env "$key_env"
  --format json --output "$GITHUB_WORKSPACE/evidence/review.json"
)
if [[ -n "${AEGIS_MODEL:-}" ]]; then args+=(--agent-model "$AEGIS_MODEL"); fi
if [[ -n "${AEGIS_BASE_URL:-}" ]]; then args+=(--agent-base-url "$AEGIS_BASE_URL"); fi
if [[ "${AEGIS_ALLOW_CUSTOM_ENDPOINT:-false}" == true ]]; then args+=(--agent-allow-custom-endpoint); fi

set +e
"$GITHUB_WORKSPACE/bin/aegis" review "${args[@]}"
review_exit=$?
set -e
printf 'exit-code=%s\n' "$review_exit" >> "$GITHUB_OUTPUT"
if [[ "$review_exit" != 0 ]]; then
  printf '::warning title=Aegis review incomplete::Review exited with code %s; the publisher will preserve diagnostics and independently evaluate the configured merge policy.\n' "$review_exit"
fi
