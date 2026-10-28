### Changed

- **Breaking: magus indexes Go itself and drops the external SCIP indexers.** Go is indexed
  in process, re-checking only the packages an edit moved; scip-go, scip-typescript,
  scip-python and rust-analyzer no longer run, so TypeScript, Python and Rust have no
  symbol index. A lookup that finds nothing names each such project as outside coverage,
  `web (no symbol index for typescript)`, and answers unknown rather than absent. A
  function defined per platform (`mem_darwin.go`, `mem_linux.go`) is one symbol per file,
  so each body is measured. Stored indexes are rebuilt once in magus's own format; SCIP is
  an export: `magus graph export -o scip > index.scip` writes the workspace's symbol index
  as one SCIP index for tools that read it.
