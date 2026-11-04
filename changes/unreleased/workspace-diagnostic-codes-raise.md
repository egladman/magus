### Changed

- **`magus\raise` takes a `DiagnosticCode`, not a string.** Mint the code with
  `magus\diagnosticCode`; `raise` checks it again against the workspace prefix.
