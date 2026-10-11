### Added

- **`testkit.CaptureStdout`, `CaptureStderr` and `Workspace.Executable`.** A capture drains
  while the function runs, so large output cannot block it, and restores the stream even
  when the test fails inside it. `Executable` writes a 0755 stand-in script. A workspace
  root now has its symlinks resolved, so it matches the paths magus reports on macOS.
