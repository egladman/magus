### Changed

- **Breaking: magus indexes Go itself and drops the external SCIP indexers.** Go is indexed
  in process, re-checking only the packages an edit moved; scip-go, scip-typescript,
  scip-python and rust-analyzer no longer run, so TypeScript, Python and Rust have no
  symbol index. A lookup that finds nothing names each such project as outside coverage,
  `web (no symbol index for typescript)`, and answers unknown rather than absent. Stored
  indexes are rebuilt once in magus's own format.
