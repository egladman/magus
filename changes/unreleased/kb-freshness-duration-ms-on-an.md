### Changed

- **`duration_ms` on an agent command row is how long the guard took to judge the call.** A
  pre-call hook never sees the call's own run time, so this is not tool time.
