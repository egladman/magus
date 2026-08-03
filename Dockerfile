# syntax=docker/dockerfile:1
#
# magus image (CGO / glibc).
#
# Build locally:
#   docker buildx build -t magus:dev .
#
# Run against the host's working directory:
#   docker run --rm -v "$PWD":/workspace magus:dev ls
#
# Built with CGO_ENABLED=1 on gcr.io/distroless/cc:nonroot (glibc + libgcc),
# -s -w -trimpath for a small, reproducible binary. For a smaller pure-Go,
# multi-arch variant see Dockerfile.static.

FROM golang:1.25 AS builder
WORKDIR /src

# inotify-tools for fs.watch. magus is pure Go (the gopherbuzz interpreter) with
# no native dependencies, so CGO here is just glibc dynamic linking.
RUN apt-get update -q && apt-get install -y inotify-tools

# Cache module fetches independent of the source tree.
COPY go.mod go.sum ./
# The local modules go.mod `replace`s. `go mod download` reads each replacement's own
# go.mod to build the module graph, so without these it fails with "reading
# libs/<name>/go.mod: no such file or directory" and takes the whole release build with
# it. Only the manifests are copied, so this layer still caches on a source-only change.
#
# Listed explicitly rather than globbed: COPY --parents would cover a module added later,
# but it needs a newer Dockerfile frontend than this has been built against, and this is
# the one build path that cannot afford an untested flag. TestDockerfilesCopyLocalReplaces
# fails when a replace directive has no line here, so the list cannot drift unnoticed.
COPY libs/diagnostics/go.mod libs/diagnostics/
COPY libs/gopherbuzz/go.mod libs/gopherbuzz/
RUN --mount=type=cache,target=/root/.cache/go-build \
    --mount=type=cache,target=/go/pkg/mod \
    go mod download

COPY . .

ARG VERSION=dev
ARG COMMIT=unknown
ARG DATE=unknown
ENV CGO_ENABLED=1
# Enable the experimental encoding/json/v2 codec for faster JSON marshaling.
# The build tag goexperiment.jsonv2 is set automatically by the toolchain when
# this variable is present; the internal/util JSON shim falls back to v1
# if GOEXPERIMENT is unset, so local builds without this variable still work.
ENV GOEXPERIMENT=jsonv2

RUN --mount=type=cache,target=/root/.cache/go-build \
    --mount=type=cache,target=/go/pkg/mod \
    go build \
      -trimpath \
      -ldflags="-s -w -buildid= \
        -X main.version=${VERSION} \
        -X main.commit=${COMMIT} \
        -X main.date=${DATE}" \
      -o /out/magus \
      ./cmd/magus

# distroless/cc provides glibc + libgcc for the CGO (dynamically linked) build.
FROM gcr.io/distroless/cc:nonroot

LABEL org.opencontainers.image.source="https://github.com/egladman/magus"
LABEL org.opencontainers.image.description="magus — content-addressed monorepo build cache"
LABEL org.opencontainers.image.licenses="Apache-2.0"

COPY --from=builder /out/magus /magus

USER nonroot:nonroot
WORKDIR /workspace

ENTRYPOINT ["/magus"]
CMD ["ls"]
