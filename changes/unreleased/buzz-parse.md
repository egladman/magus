### Added

- **`buzz\parse(path)` reads a Buzz file as data.** A new `buzz` host module returns a
  `BuzzFile` from the parser magus runs: each function and method with its extent, its `!>`
  error type and the lines of its `throw` statements, plus the top-level statements and the
  test blocks. It never raises. A file that cannot be read or parsed comes back with `error`
  set. `embedded = true` parses the way a magusfile's imports load.
