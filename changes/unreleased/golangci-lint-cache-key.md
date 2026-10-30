### Changed

- **The `golangci-lint` op keys on the linter's build and the Go toolchain selectors.**
  It observes `golangci-lint version --debug` and keys on `GOROOT`, `GOTOOLCHAIN`,
  `GOEXPERIMENT` and `GOFLAGS`.
