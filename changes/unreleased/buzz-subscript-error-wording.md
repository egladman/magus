### Fixed

- **Buzz subscript errors use upstream's wording.** A list index out of range, read or
  assigned, fails with "Out of bound list access", and a negative `str` index with "Out
  of bound string access", in place of "list index N out of range". Each still names the
  index and the length.
