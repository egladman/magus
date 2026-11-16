### Changed

- **Breaking: a local `ci` under rw repairs a declared writer that fails its check,
  then fails until the repair is committed.** Every pass still verifies without `rw`,
  and MGS3057 names the changed files. `--no-default-charms`, as a CI runner passes
  it, and a sized `magus affected ci` verify only. `magus x ci` applies
  `default_charms`.
