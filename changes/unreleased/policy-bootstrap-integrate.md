### Changed

- **The worktree bootstrap can build.** `--build-from <checkout>` runs the checkout's
  `go-build` and then places that binary; a session holding a job lease is refused.
