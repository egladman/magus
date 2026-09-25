### Changed

- **Breaking: the GitHub merge queue requires its own GitHub App.** `magus queue describe
  --status-context` and `apply` refuse to run without `--app`, and nothing falls back to
  the job's Actions token: a run or merge that token makes starts no workflow, so the
  queue validated changes and merged none. Register the app with the link `describe`
  prints.
