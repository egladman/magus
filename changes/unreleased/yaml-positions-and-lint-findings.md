### Added

- **`yaml\positions` and a typed `Finding`.** `yaml\positions(source)` returns the line and
  column of every value, keyed by JSON pointer, so a Buzz check over `yaml\parse` output
  can point at a line. `import "magus/lint"` brings `Finding`, the record a Buzz lint
  rule returns.
