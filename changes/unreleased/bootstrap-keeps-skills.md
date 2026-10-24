### Fixed

- **`bootstrap-checkout --gate-ready` keeps fresh untracked skill installs.** It removes an
untracked skill install only when its stamp differs from the one the placed `./magus` writes.
`--restart` now works with `--gate-ready` and ends with the daemon started, and the daemon
stops before the build, so a project lock it holds no longer fails the build with exit 75.
