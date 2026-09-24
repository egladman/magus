### Added

- **`magus diff` reports where a changed symbol departs from how the workspace declares the
  same kind of thing:** a missed naming pattern, a name a target already has, a rename's
  leftover old name, or reversed parameters. Each is a `Check` on
  `DiffSymbol.checks` stating the workspace's own counts, gated by
  `--conformance-min-cohort` and `--conformance-min-share`.
