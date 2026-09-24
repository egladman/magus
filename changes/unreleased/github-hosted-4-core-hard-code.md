### Changed

- **The GitHub-hosted 4-core hard-code is removed.** A larger runner gets its real core
  count. magus reads no environment variable to guess it runs in CI, so the same command
  behaves the same everywhere, and `concurrency_profile` stays `balanced`
  (`min(cores, 8)`) unless something asks otherwise.
