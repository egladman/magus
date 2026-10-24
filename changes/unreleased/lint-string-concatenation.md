### Added

- **Lint reports a string built from a chain of `+`.** The repo-only `stringjoin` linter
  flags a chain of three or more operands with one that is not a constant, and names the
  fix: `fmt.Sprintf`, `filepath.Join` for a path, or a raw string or `text/template` for
  multi-line text. The 801 files that already held one are listed as a backlog, so only
  new code is held to it.
