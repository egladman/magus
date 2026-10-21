### Fixed

- **A lint run without the `rw` charm no longer rewrites generated files.** The `generate`
  gate that runs before `lint` reported drift but left the regenerated files in the tree.
  It now restores every path it touched, on drift and on a generator error, then reports
  the same `MGS4006`. Run `magus run generate:rw` to write them.
