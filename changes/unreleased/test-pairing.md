### Changed

- **BREAKING: every Go test file pairs with a source file of the same name.** The
  new `testpair` linter in `libs/testlayout` reports each `X_test.go` with no `X.go`
  or platform family beside it, and has no exemptions: no conventional names, no
  allow list, no marker. It runs on every module, reads build-tagged files, and a
  `//nolint:testlayout` does not reach it. `testlayout` drops `allow`,
  `report-unpaired`, `honor-marker` and `pair-benchmarks`.
