### Fixed

- **Buzz `s[i]` indexes bytes, as upstream Buzz does.** It indexed runes, so on a
  non-ASCII string it disagreed with `len()`, `sub()` and `foreach`: `"aé—b".len()` is 7,
  yet `"aé—b"[5]` failed as out of range. Each index now yields a one-byte `str`, and one
  past the end fails with upstream's "Out of bound str access".
