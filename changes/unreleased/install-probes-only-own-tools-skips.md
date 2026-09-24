### Changed

- **`install` probes only its own tools and skips dependency order.** A project's install
  no longer waits for its dependencies' installs, and `run install` probes node and pnpm,
  not tsc.
