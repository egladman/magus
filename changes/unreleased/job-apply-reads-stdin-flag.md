### Changed

- **`magus job apply` reads stdin with `--stdin`, as `job fork` does.** `-f` names a file
  only, and `-f -` is refused, naming `--stdin`. Every hint that printed `job apply -f -`
  prints `job apply --stdin`.
