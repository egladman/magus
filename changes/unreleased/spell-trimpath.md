### Changed

- **`go-test` and `go-vet` pass `-trimpath`.** Matching `go-build`, so a run in one
  worktree can share its compile and result cache with another worktree at the
  same content, regardless of either one's absolute path.
