### Changed

- **Breaking: `magus server status -o json` nests concurrency.** `config.concurrency` is an object
  of `configured`, `profile` and `effective`; `config.concurrency_effective` is gone.
