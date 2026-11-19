### Added

- **An exported accessor that returns its own slice or map fails lint.** The `accessorclone`
  analyzer names `slices.Clone`, `maps.Clone` or an iterator as the fix.
  `hack/lint/accessor-clone.jsonl` lists the 16 older accessors and only shrinks; a
  `//nolint:accessorclone` is itself reported.
