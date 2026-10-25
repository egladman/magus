### Added

- **A claim record can ask for every caller of a symbol.** A `claim` entry naming
  `callers: <symbol>` expands into one `<file>#<declaration>` request per declaration
  holding an occurrence, and the store proves each one. An unresolved or ambiguous
  symbol, a stale index, an occurrence outside any declaration and an entry with both a
  path and callers are refused by name.
