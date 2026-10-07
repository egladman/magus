### Added

- **The `fieldwise` linter reports a test asserting a struct one field at a time.**
  It flags assertions that name every field of a value, which one whole comparison
  replaces; `report-partial` also flags a subset. This repo enables it, and
  testifylint's `require-error` for `NoError`, after sweeping both.
