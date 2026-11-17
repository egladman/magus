### Added

- **The `libs/safetyscissors/sandbox` module confines a child on macOS.** It runs it under
  `/usr/bin/sandbox-exec` with a deny-by-default profile generated from the rules, without
  cgo. magus's sandbox modes do not use it yet.
