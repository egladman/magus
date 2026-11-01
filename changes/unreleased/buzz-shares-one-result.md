### Added

- **`import "result"` gives every Buzz session one `Result::<T>`.** It mirrors `types.Result` and
  comes with `ok`, `fail` and `caught`. A spell, a hack script and a hook share it, and importing
  it does not load the magus namespace.
