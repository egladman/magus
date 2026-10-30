### Changed

- **`go-build` passes `-trimpath`.** Checkouts of one source share build cache
  entries, as `go-test`, `go-vet` and `go-fuzz` already did.
- **The `golangci-lint` op keys on the linter's build and the Go toolchain selectors.**
  It observes `golangci-lint version --debug` and keys on `GOROOT`, `GOTOOLCHAIN`,
  `GOEXPERIMENT` and `GOFLAGS`.
