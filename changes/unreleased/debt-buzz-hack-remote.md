### Changed

- **`hack/remote/on-actions.buzz` and `on-linux.buzz` return errors as values.** Their helpers
  return a `Result` and only the script `main` throws; the no-throw baseline rows for both fall.
  `on-actions.buzz` names its `--result` step `answer`, since `result` is now the module.
