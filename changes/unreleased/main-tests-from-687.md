### Fixed

- **`--dry-run` evaluates a magusfile that calls `charged` or `decided`.** The dry host stubs
  both methods, as the real bindings register them. `MAGUS_PROC_TREE` is listed among the
  environment variables, and the guard's out-of-budget notice names `magus status` again.
