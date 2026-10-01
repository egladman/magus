### Fixed

- **The Buzz checker rejects four programs upstream Buzz refuses.** Each used to pass
  `magus buzz --check` and fail at run time with no line, or run with a wrong answer:
  - `std.print(...)`: a module's members take a backslash; the error names
    `std\print`. Upstream-strict sessions only; magusfiles keep reading the dot form.
  - an unknown method on a list, map or string (`xs.push(1)`, `s.toUpperCase()`): the
    error names the builtin that does the job (`lists have append`).
  - `len(xs)`: `len` is a method; the error says `xs.len()`.
  - `return null` from a function declared `> int`: the error says to declare `> int?`.
- **`magus\cmd`, `magus\run` and `magus\describe` honor `opts.allow_failure`.** A
  non-zero exit returns the result instead of raising, as `proc\exec` does.
