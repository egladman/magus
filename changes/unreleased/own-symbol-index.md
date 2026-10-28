### Changed

- **Breaking: magus indexes Go itself and drops the external SCIP indexers.** Go is indexed
  in process, re-checking only the packages an edit moved; scip-go, scip-typescript,
  scip-python and rust-analyzer no longer run, so TypeScript, Python and Rust have no
  symbol index. Stored indexes are rebuilt once in magus's own format; SCIP is an export.
