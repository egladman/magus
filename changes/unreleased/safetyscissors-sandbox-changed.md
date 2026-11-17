### Changed

- **Breaking for SDK callers: the sandbox's confinement lives in
  `libs/safetyscissors/sandbox`.** `internal/sandbox/filesystem` moved whole, and
  `sandbox.ABI`, `RequiredABI` and `LauncherFiles` are the module's. A host without a
  kernel sandbox answers `*sandbox.UnsupportedError`.
- **Breaking for SDK callers: a module policy is `sandbox.New([]sandbox.Bind,
  ...Option)`.** It defaults to `Required` and narrows to a job lease with
  `sandbox.LeaseBoundary`.
- **The landlock-required run is `libs/safetyscissors`'s `landlock-test`.** It reads
  `SAFETYSCISSORS_TEST_REQUIRE_LANDLOCK`; the root `landlock-test` is gone.
