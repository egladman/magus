### Changed

- **The doctrine states "Everything is a file".** Local state is a directory of plain files: one
  file per independent entry, one row per line, written by rename, readable with `cat`, `grep`,
  `ls` and `diff`, never a JSON document. The entry lists the stores that do not follow it yet,
  the job store and runtime history first.
