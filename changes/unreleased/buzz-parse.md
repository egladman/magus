### Added

- **`buzz\parse(path)` reads a Buzz file as data.** A new `buzz` host module returns a
  `BuzzFile` from the parser magus runs: each function and method with its extent, `!>` type
  and `throw` lines, plus the top-level statements and test blocks. It never raises; a file
  that fails to read or parse sets `error`. `embedded = true` parses like a magusfile import.
