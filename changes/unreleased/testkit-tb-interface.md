### Changed

- **`testkit` and `fakespell` take `testkit.TB` instead of `testing.TB`.** It is the part of
  `testing.TB` they call, so `*testing.T` and `*testing.B` still satisfy it, and a program
  with no test running (an `Example`) can supply a few-line type of its own.
