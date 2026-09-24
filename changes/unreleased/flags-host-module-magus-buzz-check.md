### Added

- **A `flags` host module and `magus buzz --check`.** `flags\parse` returns
  `{values, positionals, unknown}`. `--check` parses and type-checks without running; add
  `--embedded` for magusfile code.
