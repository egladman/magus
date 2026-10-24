### Added

- **A claim record can ask for every caller of a symbol.** A `claim` entry of
  `magus job apply` that names `callers: <symbol>` instead of a path expands into one
  request per declaration holding an occurrence of the symbol, path `<file>#<declaration>`
  because `refs:<symbol>`, and the store proves each one as it proves a request written by
  hand. A symbol the index does not resolve, or resolves to several, a stale index, and an
  occurrence no declaration holds are refused by name, as is an entry with both a path and
  callers.
