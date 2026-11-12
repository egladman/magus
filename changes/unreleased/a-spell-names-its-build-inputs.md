### Added

- **A spell names a relinked build's inputs: `export fun mgs_getScopes() > Command`.** The
  command prints one doublestar glob per line, absolute or relative to the project, and a
  leading `!` excludes. magus appends the package to its args and runs it in the directory
  of the project whose target relinks `./magus`. A changed path is a build input when an
  include matches it and no exclusion does; a glob outside the workspace drops out.
- **The go spell declares `mgs_getScopes`.** It runs `go list -e -deps` and prints each
  linked package's `*.go`, less `*_test.go`, the files it compiles or embeds, its embed
  patterns, and its module's `go.mod`, `go.sum`, `go.work` and `go.work.sum`.

### Changed

- **The build inputs of `./magus` come from the spell, not from Go in core.** `magus job
  fork` (the landing bootstrap) and the symbol index's binary-skew check ask the spells of
  the project that relinks `./magus`. No relinking target, no spell declaring
  `mgs_getScopes`, or a workspace that did not load makes the inputs unknown, and each says
  so; none reads as unchanged. Core no longer runs `go list` or sets `GOEXPERIMENT`.
- **The Go input digest go-build links into `./magus` hashes the files the go spell's
  `mgs_getScopes` names.** It now also covers each in-tree module's `go.mod` and `go.sum`
  and a package's embed patterns, so a binary stamped before this change does not match
  and is rebuilt once.
