### Fixed

- **The Go symbol index holds every OS's files on every host.** A spell's
  `SymbolIndexer` takes `envs`, one environment overlay per run, and magus merges the
  runs into the one index. The go spell runs scip-go under GOOS linux, darwin and
  windows, so `*_linux.go` reaches `magus refs` on a Mac and `*_windows.go` reaches it
  anywhere. A function defined once per OS keeps each file's doc beside that file.
