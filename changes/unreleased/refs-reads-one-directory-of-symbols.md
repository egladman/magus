### Changed

- **`refs` reads one directory's symbols, not the whole index.** Symbol shards are split
  by the directory a symbol is defined in, a bare name routes to the shards of the symbols
  it names, and `--occurrences` reads each symbol's sites from a file written once per
  index instead of decoding the SCIP index on every call.
