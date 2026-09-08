# Contributing

Start a new branch from current `master`, keep changes reviewable, and submit a pull request. Never commit API keys, local `.aegis.json`, private PR reports or `.env` files.

Use the Go version in `go.mod` or a compatible newer version. The core has no third-party runtime Go dependencies.

```sh
gofmt -w ./cmd ./internal ./tools
go vet ./...
go test -race ./...
go build -o /tmp/aegis ./cmd/aegis
/tmp/aegis eval --corpus eval/cases --format json --output /tmp/aegis-replay.json
/tmp/aegis eval-live --corpus eval/live --aegis-binary /tmp/aegis \
  --agent-provider none --format json --output /tmp/aegis-live.json
```

Replay regression failures block CI. Live synthetic scenarios may expose genuine detector misses; preserve the independently labelled expected result and report the miss, rather than rewriting labels to make the current implementation pass. New provider tests should use local HTTP fixtures by default and never spend API credits in ordinary unit tests.

Security-sensitive changes need negative tests for their trust boundary: credentials/redirects for providers, source/path containment for tools, false corroboration for Verifier, stale head/permissions for publishing, and network/filesystem access for sandbox execution. Run Docker integration tests on Linux using the supplied trusted analysis image; see `scripts/analysis.Dockerfile` or the current workflow image-build step.

The Go CLI emits an evidence report; the `github` publisher independently computes the gate. Keep unknown, skipped, degraded and failed states visible. Do not convert missing evidence into a clean result.
