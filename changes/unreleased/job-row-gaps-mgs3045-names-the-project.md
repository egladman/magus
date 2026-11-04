### Fixed

- **MGS3045 names the project that owns the write.** A write under `libs/x` gated on
  `test .`, which runs none of its tests. The gate is now `test libs/x`, and a check of
  `test .` does not satisfy it.
