### Changed

- **Change risk reads the coverage profile a project's tests already write.**
  `mgs_getCoverage` now declares `{format, profile}`, and the project target that
  declares the profile as an output writes it. At head that target is freshened through
  the cache, so ci tests the tree once. At base the profile is kept per merge-base commit
  in the workspace store, and a miss runs the target once in a copy of the base and warns
  that it did. The reserved `coverage` op, `MAGUS_COVERAGE_DIR`, the coverage `command`,
  `file`, `test` and `uses` fields, and the 7 minute budget are removed.
