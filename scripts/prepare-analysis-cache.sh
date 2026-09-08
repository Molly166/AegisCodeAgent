#!/usr/bin/env bash
set -euo pipefail

target=${1:?target repository required}
cache=${2:?isolated dependency cache required}
[[ "$target" = /* && -d "$target/.git" && ! -L "$target" ]]
[[ "$cache" = "${RUNNER_TEMP:?}/aegis-modcache" && ! -e "$cache" ]]
[[ "$target" != *,* && "$cache" != *,* ]]
mkdir -m 700 "$cache"

# Downloading a module does not execute its Go code. Force the public proxy,
# disable toolchain switching, and never mount private module credentials.
# Failed downloads never fall back to unrestricted host execution.
if [[ -f "$target/go.mod" && ! -L "$target/go.mod" ]]; then
  docker run --rm --read-only --cap-drop=ALL --security-opt=no-new-privileges \
    --pids-limit=128 --memory=2g --cpus=2 \
    --user "$(id -u):$(id -g)" \
    --mount "type=bind,src=$target,dst=$target,readonly" \
    --mount "type=bind,src=$cache,dst=/go/pkg/mod" \
    --tmpfs /tmp:rw,nosuid,nodev,size=512m \
    --workdir "$target" --entrypoint /usr/local/go/bin/go \
    --env HOME=/tmp --env GOCACHE=/tmp/go-build --env GOMODCACHE=/go/pkg/mod \
    --env GOPROXY=https://proxy.golang.org --env GOSUMDB=sum.golang.org \
    --env GOTOOLCHAIN=local --env GOWORK=off --env CGO_ENABLED=0 \
    aegis-analysis:local mod download
fi
