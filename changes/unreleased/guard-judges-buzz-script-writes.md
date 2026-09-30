### Fixed

- **The agent guard judges a Buzz script's writes, and leaves scratch files alone.**
  `magus buzz` replacing a file the workspace already has through `fs` is refused like
  the same write from python. `sed -i` or a scripted rewrite whose files all lie outside
  the workspace passes, including after a `cd` into a scratch directory.
