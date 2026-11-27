### Fixed

- **`job wait` accepts a `:rw` run that rewrote only the files it declares it edits.** A
  passing run whose moved inputs are all its declared updates records `settled_key`, the
  key its inputs hash to after the run, and a job judges the run current at that key as
  well as its own. A hand edit after the run still makes it stale. Nothing is cached under
  the settled key.
  [ADR 0053](https://eli.gladman.cc/magus/decisions/0053-an-op-declares-the-files-it-edits-in-place/).
