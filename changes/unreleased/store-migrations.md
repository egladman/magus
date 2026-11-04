### Added

- **The job store migrates itself.** A change to a stored member's name ships as a
  migration: the first magus that opens an older `jobs.jsonl` rewrites every row once,
  through a rename, records the migration in the header and prints what it migrated. A
  store past the migrations a magus knows is refused with
  [MGS3051](https://eli.gladman.cc/magus/reference/codes/sandbox/MGS3051/) naming both.
  [State](https://eli.gladman.cc/magus/concepts/state/#store-migrations).
