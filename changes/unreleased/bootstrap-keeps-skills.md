### Fixed

- **`bootstrap-checkout --gate-ready` keeps fresh untracked skill installs.** It removes
  one only when its stamp differs from what the placed `./magus` writes. `--restart` works
  with `--gate-ready`, and the daemon stops before the build, so its project lock no
  longer fails the build with exit 75.
