### Fixed

- **`magus queue describe --app` works for a private GitHub App.** It reads the App ID
  from an organization's installations where an owner's token lists them; elsewhere it
  names the app's settings page and prints the rerun with `--app <slug>:<id>`. The printed
  key step reads no stdin and deletes the download even when storing fails.
