### Fixed

- **`describe job` and `job wait` no longer panic on a job with postconditions.** A command that
  loaded the workspace under `--root` as given and again under the path it resolves to
  hit the one-workspace check; both spellings now name one load.
