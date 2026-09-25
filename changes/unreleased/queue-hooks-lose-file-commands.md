### Security

- **Queue hooks lose the GitHub Actions file commands.** `GITHUB_ENV`, `GITHUB_PATH`,
  `GITHUB_OUTPUT` and the runner paths later steps trust are removed from a hook's
  environment, beside the tokens. A unit starting with `-` or holding a line break is
  refused, and a hook's process group is waited on without racing its reaping.
