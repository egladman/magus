### Fixed

- **`magus queue describe --app` works for a private GitHub App.** GitHub hides one from
  every token but its installation's, so describe no longer fails: its steps ask for the
  client id, and the new `--app-id` takes the App ID the status is pinned to.
