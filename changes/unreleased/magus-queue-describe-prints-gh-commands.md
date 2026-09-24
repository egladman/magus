### Added

- **`magus queue describe` prints the `gh` commands that finish setting the queue up.**
  `--app <slug>` adds the steps for the queue's own GitHub App, which `setup-magus`
  turns into a token. magus runs none of it. `apply` refuses a status pinned to another
  integration than its token's (MGS3019).
