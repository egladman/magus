### Fixed

- **`magus run go-vet . -- -tags e2e ./pkg/` scopes the run.** Arguments after `--`
  drop go-vet's `./...`, so a run with flags names its package too. `GOFLAGS`, `GOEXPERIMENT`
  and `CGO_ENABLED` now key go-build, go-test, go-vet and go-fuzz, so a tagged run no
  longer replays as a plain one.
