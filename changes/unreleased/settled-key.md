### Fixed

- **`job wait` accepts a `:rw` run that rewrote only its declared edits.** Such a passing
  run records `settled_key`, the key its inputs hash to afterward, and a job counts the run
  current at that key or its own. A hand edit after the run still makes it stale. Nothing
  is cached under the settled key.
  [ADR 0053](https://eli.gladman.cc/magus/decisions/0053-an-op-declares-the-files-it-edits-in-place/).
