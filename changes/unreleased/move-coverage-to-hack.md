### Changed

- **`coverage.buzz` moved from the repository root to `hack/magusfile/`.** It is a
  module the root magusfile imports, with no `main`, so it sits beside the other
  magusfile modules. `buzz-test` now runs its test blocks embedded, like the rest of
  `hack/magusfile/`.
