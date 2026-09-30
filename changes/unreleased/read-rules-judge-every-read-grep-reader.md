### Added

- **`grep-reader` refuses grep as a reader.** A definition lookup with a context
  flag (`grep -A40 'func X' f.go`) is served `magus refs X --definition --source`,
  or the declaration's own lines when the index cannot vouch for the name.
