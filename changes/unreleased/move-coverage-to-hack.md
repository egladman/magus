### Changed

- **The root magusfile's three modules moved from the repository root to
  `hack/magusfile/`.** `coverage.buzz` keeps its name, `badge.buzz` is now
  `badges.buzz` and `releaser.buzz` is now `releases.buzz`. Each is a module the root
  magusfile imports, with no `main`, so it sits beside the other magusfile modules.
  `buzz-test` now runs the coverage and badge test blocks embedded, like the rest of
  `hack/magusfile/`.
