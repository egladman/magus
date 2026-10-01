### Fixed

- **`magus describe harness` retires a stale host entry it wrote that names no template.**
  A rewritten Claude Code session PATH entry was merged in beside the old one, and the
  plan then called the file current. An entry that names magus without running it is now
  magus's to replace; a hook that runs magus directly stays yours.
