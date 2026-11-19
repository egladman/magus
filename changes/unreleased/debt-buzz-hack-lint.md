### Changed

- **The `hack/lint` rules return errors as values.** A helper that read a file,
  parsed YAML or matched a glob now returns `result\Result` instead of declaring
  `!>`, and only each rule's `check` throws. The `buzz-error-signature` baseline
  rows for `hack/lint` are gone.
