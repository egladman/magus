### Fixed

- **`magus run go-vet . -- -tags e2e ./pkg/` scopes the run.** Arguments after `--`
  replace go-vet's `./...` default, as they do for go-test. `GOFLAGS`, `GOEXPERIMENT`,
  `CGO_ENABLED` and `GOTOOLCHAIN` now key go-build, go-test, go-vet and go-fuzz, so a
  tagged run no longer replays as a plain one.
