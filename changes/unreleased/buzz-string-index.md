### Fixed

- **Buzz `s[i]` indexes bytes, as upstream Buzz does.** It indexed runes, so on a
  non-ASCII string it disagreed with `len()`, `sub()` and `foreach`: `"aé—b".len()` is 7,
  yet `"aé—b"[5]` failed as out of range. Each index now yields a one-byte `str`, and one
  past the end fails with upstream's "Out of bound str access".
- **Buzz subscript errors use upstream's wording.** A list index out of range, read or
  assigned, fails with "Out of bound list access", and a negative `str` index with "Out
  of bound string access", in place of "list index N out of range". Each still names the
  index and the length.
