# Official Go image, pinned to a multi-platform immutable manifest.
FROM golang:1.26.6-bookworm@sha256:116d58cbd88c1297624acc6e967a060012422bacf9930927e23fb719189c6f36

ENV GOTOOLCHAIN=local CGO_ENABLED=0 GOWORK=off
RUN GOBIN=/usr/local/bin go install honnef.co/go/tools/cmd/staticcheck@v0.7.0 \
    && GOBIN=/usr/local/bin go install github.com/securego/gosec/v2/cmd/gosec@v2.28.0

# Runtime restrictions and explicit mounts are imposed by DockerRunner.
# No repository files or credentials are copied into the image.
WORKDIR /tmp
