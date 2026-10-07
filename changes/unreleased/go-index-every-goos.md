### Fixed

- **The Go symbol index holds every OS's files on every host.** A spell's
  `SymbolIndexer` takes `envs`, one environment overlay per run, and magus merges the
  runs into the one index. The go spell runs scip-go under GOOS linux, darwin and
  windows, and a function defined once per OS keeps each file's doc beside that file.
