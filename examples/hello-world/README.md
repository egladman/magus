# magus hello-world

A four-project Go workspace. Each `svc-*/` directory is its own Go
module that prints its name. The example exists to demonstrate how
magus discovers projects automatically and fans out cached build steps
across them.

There is no `magus.yaml` and no Go-side registration code — magus reads
each `go.mod` and synthesises a project with stock `build`/`test`/`lint`
handlers from `magus/pack/golang`.

## Try it with Docker (no local Go required)

```sh
# from this directory
docker run --rm -v "$PWD":/workspace ghcr.io/egladman/tack/magus:latest list
```

Expected output (the order may vary):

```
workspace: /workspace (4 projects)

project: svc-a
  dir:  /workspace/svc-a
  lang: go
  ...

project: svc-b
  ...
```

To fan out a real build (requires a Go toolchain — drop the
`distroless/static` image and use the official `golang` image, or run
locally):

```sh
magus --concurrency 4 run build
```

Each project is built in parallel up to `--concurrency`, and results
are content-addressed: a second invocation replays the snapshots
instead of recompiling. With `magus run build`'s output you should
see four `↻ svc-X (miss, ...)` lines on the cold run and four
`✓ svc-X (hit, ...)` lines on the warm one.

## Try it with telemetry (your own collector)

```sh
docker run --rm \
  -e MAGUS_TELEMETRY_ENABLED=true \
  -e MAGUS_TELEMETRY_ENDPOINT=host.docker.internal:4317 \
  -e MAGUS_TELEMETRY_INSECURE=true \
  -v "$PWD":/workspace \
  ghcr.io/egladman/tack/magus:latest run build
```

`magus.cache.hit`, `magus.cache.miss`, and `magus.cache.duration`
metrics will land at the OTLP collector you point `MAGUS_TELEMETRY_ENDPOINT`
at. Magus does not run a hosted backend — the collector is yours.

## Inspect the configured state

```sh
docker run --rm -v "$PWD":/workspace ghcr.io/egladman/tack/magus:latest status
```

Prints telemetry, cache config, and (if any parent magus is running)
the live concurrency pool.
