### Added

- **A release refuses while a development-only shim remains.** A shim marked
  `compat(until: the next release)` names each `file:line` in `magus run release` and
  stops `release-build`.
