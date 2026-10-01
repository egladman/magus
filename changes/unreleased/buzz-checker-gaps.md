### Fixed

- **The Buzz checker rejects four programs upstream Buzz refuses.** Each used to pass
  `magus buzz --check` and fail at run time with no line, or run with a wrong answer:
  - `std.print(...)`: a module's members take a backslash; the error names
    `std\print`. Upstream-strict sessions only; magusfiles keep reading the dot form.
  - an unknown method on a list, map or string (`xs.push(1)`, `s.toUpperCase()`): the
    error names the builtin that does the job (`lists have append`).
  - `len(xs)`: `len` is a method; the error says `xs.len()`.
  - `return null` from a function declared `> int`: the error says to declare `> int?`.
- **A call to a member an untyped value lacks names the member and the line.** It raised
  `null is not callable` with no position; it now raises
  `<file>:<line>: unknown method push on list`.
- **A diagnostic inside an interpolated string points at the expression.** Every error
  in `"{...}"` reported line 1, column 1.
- **`magus\cmd`, `magus\run` and `magus\describe` honor `opts.allow_failure`.** A
  non-zero exit returns the result instead of raising, as `proc\exec` does.
