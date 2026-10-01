### Fixed

- **The Buzz checker rejects four programs upstream Buzz refuses.** Each used to pass
  `magus buzz --check` and fail at run time with no line, or run with a wrong answer:
  `std.print(...)` (upstream-strict sessions only), an unknown method on a list, map or
  string, `len(xs)`, and `return null` from a function declared `> int`. Each error names
  the fix.
