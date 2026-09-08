#!/usr/bin/env bash
# Run on Linux with Go, GNU tar, gzip, zip and sha256sum (GitHub ubuntu runner).
set -euo pipefail
release_tag=${1:?existing release tag required}
[[ "$release_tag" =~ ^v[0-9]+\.[0-9]+\.[0-9]+(-[a-zA-Z0-9.-]+)?$ ]]
release_version=${release_tag#v}
release_root=$(git rev-parse --show-toplevel)
[[ -n "$release_root" && "$release_root" = "$PWD" && -f go.mod && ! -e dist ]]
release_commit=$(git rev-parse HEAD)
release_epoch=$(git show -s --format=%ct HEAD)
[[ "$release_commit" =~ ^[0-9a-f]{40}$ && "$release_epoch" =~ ^[0-9]+$ ]]
test -z "$(git status --porcelain=v1 --untracked-files=no)"
mkdir -m 755 dist
export LC_ALL=C TZ=UTC

for release_os in linux darwin windows; do
  for release_arch in amd64 arm64; do
    release_name="aegis_${release_version}_${release_os}_${release_arch}"
    release_dir="$release_root/dist/$release_name"
    mkdir -m 755 "$release_dir"
    release_binary=aegis
    if [[ "$release_os" == windows ]]; then release_binary=aegis.exe; fi
    CGO_ENABLED=0 GOOS="$release_os" GOARCH="$release_arch" go build \
      -trimpath -buildvcs=false -ldflags "-s -w -buildid= -X main.version=$release_version" \
      -o "$release_dir/$release_binary" ./cmd/aegis
    install -m 644 LICENSE "$release_dir/LICENSE"
    touch -d "@$release_epoch" "$release_dir/$release_binary" "$release_dir/LICENSE"
    if [[ "$release_os" == windows ]]; then
      (cd "$release_dir" && zip -X -q "../$release_name.zip" LICENSE "$release_binary")
    else
      tar --sort=name --mtime="@$release_epoch" --owner=0 --group=0 --numeric-owner \
        -C "$release_dir" -cf - LICENSE "$release_binary" | gzip -n > "dist/$release_name.tar.gz"
    fi
  done
done
(cd dist && sha256sum ./*.tar.gz ./*.zip > checksums.txt)
node -e 'const fs=require("node:fs"); fs.writeFileSync("dist/build-info.json", JSON.stringify({version:process.argv[1], commit:process.argv[2], source_date_epoch:Number(process.argv[3]), go:process.argv[4]}, null, 2)+"\n")' \
  "$release_version" "$release_commit" "$release_epoch" "$(go version)"
