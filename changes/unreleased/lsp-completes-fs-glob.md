### Changed

- **The language server completes, hovers and signs `fs\glob`.** It read only `fs.glob`,
  the spelling the checker refuses; after a dot it offers nothing, since only a value's
  member follows one.
