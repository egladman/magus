### Fixed

- **A `MAGUS_*` value that does not parse stops the load.** A bad number or duration was
  ignored, and `magus.Open` skipped validating the environment at all.
