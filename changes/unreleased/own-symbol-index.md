### Changed

- **Breaking: magus indexes Go itself and drops the external SCIP indexers.** Go is
  indexed in process, re-checking only the packages an edit moved. A lookup in a language
  with no index names the project as outside coverage and answers unknown. SCIP is an
  export: `magus graph export -o scip` writes the workspace's symbol index.
