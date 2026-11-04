### Changed

- **`magus refs` refuses what it used to ignore, and takes trailing paths.** A path on
  `--definition`, `--source` or `--rename`, `--limit` on the plain list,
  `--definition --occurrences`, and `--text` with a symbol flag exit 2 and name the fix.
  `magus refs Foo some/dir` scopes the answer. `-rename` now meets the guard as `--rename` does.
