### Added

- **`image\layout(spec)` shows what an image layer holds before it is built.** It
  lists each tar entry with its path, kind, mode, owner, size and mtime, plus the Spec
  item that placed it, from the same list `image\assemble` writes. `assemble` now returns those entries
  in `added.entries`, and under `--dry-run` it returns the listing instead of an empty
  Layout.
