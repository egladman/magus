### Added

- **`knowledge.conformance.checks` takes `min_subject`.** Both alias metrics judge packages with that many importers or more; it defaults to 5. Measured on this tree: 450 of 17782 imports are aliased, 93 packages are judged, `alias-share` sits at 30 at p95, and `name-clash` is 62 for `internal/queue/types`, its one case.
