### Fixed

- **The `magus describe harness` merge command can run twice.** Once the harness is current,
  a second run writes nothing; it used to fail with `cannot iterate over null`.
