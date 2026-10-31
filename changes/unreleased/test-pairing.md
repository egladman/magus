### Changed

- **Breaking: every Go test file pairs with a source file of the same name.** The
  new `testpair` linter reports each `X_test.go` with no `X.go` or platform family
  beside it, with no exemptions. It runs on every module, reads build-tagged files,
  and `//nolint:testpairing` does not reach it. `testpairing` drops `allow`,
  `report-unpaired`, `honor-marker` and `pair-benchmarks`.
