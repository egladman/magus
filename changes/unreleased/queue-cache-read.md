### Added

- **`magus queue validate --remote-cache-read` gives hooks the signed remote cache.**
  The queue serves the GitHub Actions cache service to hooks through a loopback proxy
  that forwards lookups with the runner's token and refuses every write. Hooks get a
  stand-in token, the base's trusted keys and remote writes off; without the runner's
  credentials or a trusted key, validate refuses to start.
