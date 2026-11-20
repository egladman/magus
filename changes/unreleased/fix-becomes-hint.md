### Changed

- **Breaking: a diagnostic's `fix` is `hint`.** The JSON and YAML key, the Buzz field and
  the CLI line `hint:` replace `fix`. Go's `WithFix` is `WithHint`. No alias reads the old key.
