### Added

- **The `extended` charm**, for tests that need more of the host than the gate does.
  A function target reads it with `ctx.hasCharm("extended")`. This repository's cgo
  compression and shell completion tests now run only under it.
