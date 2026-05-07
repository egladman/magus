# comparison

Eight identical Go services used to compare naive `make` builds against
`magus run build`. `make` recompiles every service on every invocation;
`magus` content-addresses each build and replays snapshots on subsequent
runs without invoking the compiler.

## Quick start (Docker, no local Go needed)

```sh
# cold build — misses, builds all 8 services in parallel, populates cache
docker run --rm -v "$PWD":/workspace ghcr.io/egladman/tack/magus:latest \
  --concurrency 8 run build

# warm build — all 8 services hit, no compiler invoked
docker run --rm -v "$PWD":/workspace ghcr.io/egladman/tack/magus:latest \
  --concurrency 8 run build
```

## Full wall-clock comparison (requires magus and make on PATH)

```sh
./bench.sh
```

The script runs five scenarios in order and prints the elapsed time for each:

| Scenario                    | Typical time | Why                                                 |
| --------------------------- | ------------ | --------------------------------------------------- |
| `make -j1` cold             | 600–900 ms   | Compiles 8 services serially                        |
| `make -j1` warm             | 600–900 ms   | make checks timestamps but still invokes `go build` |
| `magus` cold, concurrency=8 | 150–250 ms   | Parallel builds; snapshots outputs                  |
| `magus` warm, concurrency=8 | 20–60 ms     | Pure cache replay — compiler not invoked            |
| `magus` one file changed    | 80–150 ms    | Only the affected service misses; the rest hit      |

## What magus tracks that make does not

- Source file contents (not just timestamps)
- Tool versions (`go version`, compiler flags)
- Environment variables you explicitly allow into the cache key
- Upstream project hashes (only rebuild dependents when a dependency output
  actually changed, not just when it was rebuilt)
