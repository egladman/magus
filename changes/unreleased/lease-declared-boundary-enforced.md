### Added

- **A lease's declared boundary is enforced.** The guard denies writes outside
  `write_paths` or inside `deny_paths`, any write by a `read_only` row, and `ci` when
  `check` names a narrower target.
