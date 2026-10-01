### Fixed

- **`magus\cmd`, `magus\run` and `magus\describe` honor `opts.allow_failure`.** A
  non-zero exit returns the result instead of raising, as `proc\exec` does.
