### Added

- **A shard plan in a pipe waits for the magus stages before it.** `magus affected
  generate --no-default-charms | magus affected ci --plan` prints the plan only once
  generate has passed, and nothing (MGS3030) when it failed, so no shard starts from a
  drifted tree. `magus run --stdin` reading a plan refuses the same way.
