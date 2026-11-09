### Added

- **`magus job apply <id> --release-write-path <path>` drops write paths and keeps the rest of
  the spec.** It repeats or takes a comma list, and a value holding `#` is one declaration
  claim, so `--add-write-path` no longer splits `a.go#Run,Stop` either.
