### Added

- **`magus\symbols()` returns every symbol's doc comment as data.** Each record
  carries the node, source, language, name, kind, owner and the whole comment, for
  every language whose symbol index is declared, beside each index's freshness. A
  magusfile rule reads it and decides what the text must say; magus judges none of it.

### Fixed

- **The Go symbol index holds every OS's files on every host.** A spell's
  `SymbolIndexer` takes `envs`, one environment overlay per run, and magus merges the
  runs into the one index. The go spell runs scip-go under GOOS linux, darwin and
  windows, so `*_linux.go` reaches `magus refs` on a Mac and `*_windows.go` reaches it
  anywhere.
