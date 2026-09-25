### Added

- **The guard denies a retired or misspelled MAGUS_* variable (`unknown-env`).** A
  `NAME=value` prefix, `env NAME=value`, `env -u NAME` or `export` of a name magus's own
  startup would refuse (MGS1046) is denied with the same message, naming the replacement
  or the closest real name.
