### Added

- **`needs_ready` runs a tool's readiness probe before a target.** A magusfile target
  that calls a tool itself, such as `docker load` through `proc\exec`, names it:
  `"image-build": {"needs_ready": ["docker"]}`. A stopped daemon then fails the target as
  MGS3004 before its body runs. Any spell the workspace registers can probe, so the
  project need not bind it. A tool no registered spell can probe is a load error.
