### Changed

- **A spell names a relinked build's inputs: `export fun mgs_getScopes() > Command`.** The
  command prints one doublestar glob per line, and a leading `!` excludes. magus appends
  the package and runs it in the project whose target relinks `./magus`. A changed path
  matching an include and no exclusion is a build input.
- **The go spell declares `mgs_getScopes`.** It runs `go list -e -deps` and prints each
  linked package's `*.go` less `*_test.go`, what it compiles or embeds, and its module
  files.
- **The build inputs of `./magus` come from the spell, not from Go in core.** The landing
  bootstrap of `magus job fork` and the symbol index's binary-skew check ask the spells.
  No relinking target, no spell declaring `mgs_getScopes`, or an unloaded workspace makes
  the inputs unknown, and says so. Core no longer runs `go list`.
- **go-build's Go input digest hashes the files `mgs_getScopes` names.** It now covers each
  in-tree module's `go.mod` and `go.sum`, so a binary stamped before this change is rebuilt
  once.
