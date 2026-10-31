### Fixed

- **Doctor no longer reports a recorder hook as uncovered.** The guard-wiring probe now
  asks the harness declaration which wired commands bind a hook point, and expects a
  verdict only from those. `magus-reclaim-disk` on Codex and Cursor binds none and is
  silent by design. A verdict hook that goes silent still fails.
