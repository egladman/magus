### Added

- **`image\layout(spec)` shows what an image layer holds before it is built.** It
  lists each tar entry with its path, kind, mode, owner, size and mtime, plus the Spec
  item that placed it, from the same list `image\assemble` writes. `assemble` now returns those entries
  in `added.entries`, and under `--dry-run` it returns the listing instead of an empty
  Layout.

### Fixed

- **A `Tree` whose include or exclude leaves no file is an error naming its patterns.**
  Before, it wrote an image with no layer.
- **An image with no layer writes `"layers":[]`.** It wrote `"layers":null`, which
  image-spec does not allow.
- **A Tree pattern that matches a directory covers every file below it.**
  `exclude = ["etc/ssl"]` now drops `etc/ssl/ca.pem`, which it used to keep.
- **`modePolicy = executableBit` over a tree with no executable file is refused.** Every
  file would have shipped 0644 without a word.
