### Changed

- **`magus run` and `magus affected` started inside a step schedule at the machine's
  width.** They inherited the step's GOMAXPROCS, one core for a one-slot claim, though
  they take claims of their own.
