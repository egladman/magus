### Changed

- **Breaking for SDK callers: the sandbox's read, write and exec confinement lives in
  `libs/safetyscissors/sandbox`.** `internal/sandbox/filesystem` moved whole to
  `libs/safetyscissors/sandbox/filesystem`. `sandbox.ABI`, `RequiredABI` and
  `LauncherFiles` are the module's; `sandbox.ErrUnsupported` is gone, and a host without
  a kernel sandbox answers `*sandbox.UnsupportedError`. A module policy is built with
  `sandbox.New([]sandbox.Bind, ...Option)`, defaults to `Required`, and narrows to a job
  lease with `sandbox.LeaseBoundary`. The landlock-required test run is the
  `libs/safetyscissors` project's `landlock-test`, under
  `SAFETYSCISSORS_TEST_REQUIRE_LANDLOCK`; the root `landlock-test` is gone.

### Added

- **The `libs/safetyscissors/sandbox` module confines a child on macOS** under
  `/usr/bin/sandbox-exec` with a deny-by-default profile generated from the policy's
  rules, without cgo. magus's sandbox modes do not use it yet, so a magus run on macOS
  behaves as before.
