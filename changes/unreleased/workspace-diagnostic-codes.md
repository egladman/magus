### Added

- **A workspace declares its own diagnostic codes.** `diagnostics: {prefix, docs}` in
  `magus.yaml` names the workspace's code prefix and the page each code has.
  `magus\diagnosticCode("WS1003")` mints a code under that prefix, and the CLI marks a
  workspace code's origin, so it never reads as one of magus's own.
