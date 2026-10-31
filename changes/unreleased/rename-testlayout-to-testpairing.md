### Changed

- **Breaking: `libs/testlayout` is `libs/testpairing`.** The Go module path, the
  golangci-lint plugin and the linter name change with it, so a `//nolint:testlayout`
  or a `testlayout` entry in `.golangci.yml` must read `testpairing`. The analyzer
  does the same work. The old project page URL is retired, not aliased.
