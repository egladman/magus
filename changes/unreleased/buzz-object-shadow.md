### Fixed

- **A module's private object or enum stays its own when the importer declares one of
  the same name.** A script declaring `object Node` that imported `magus/figure` made
  figure build its internal nodes from the script's `Node`, so every figure refused to
  draw. Private objects and enums now get per-module keys, as private vars and functions
  already did, matching upstream Buzz.
