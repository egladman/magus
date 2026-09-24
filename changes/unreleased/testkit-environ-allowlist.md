### Added

- **`testkit.Environ`, `Isolate` and `Main` give a test an environment built from an
  allowlist.** The sandbox's allowlist plus the Go toolchain's settings survive; HOME and
  the XDG base directories move under a temp root; the broker and server are pinned off;
  everything else is dropped. A package names any extra variables in its TestMain.
