### Changed

- **Breaking: `vcs\base` is declared as raising.** It raises only for a `dir` that does
  not exist, but a caller outside a raising function must now catch it.
