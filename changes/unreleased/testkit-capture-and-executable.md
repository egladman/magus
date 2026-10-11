### Added

- **`testkit.CaptureStdout`, `CaptureStderr` and `Workspace.WriteExecutable`.** A capture drains
  while the function runs, so large output cannot block it, and restores the stream even
  when the test fails inside it. `WriteExecutable` writes a 0755 stand-in script. A workspace
  root now has its symlinks resolved, so it matches the paths magus reports on macOS.
