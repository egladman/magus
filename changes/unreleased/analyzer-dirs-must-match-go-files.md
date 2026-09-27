### Changed

- **An analyzer directory setting that matches no Go files fails the linter load.**
  This covers `providerio`'s `dirs` and the `skip-dirs` of `filenames` and
  `hostagnostic`, so a stale entry can no longer turn a rule off silently.
