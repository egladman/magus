### Fixed

- **`magus broker units` fits the broker's drain and SIGHUP.** Each unit pins
  `shutdown_grace` and waits that long plus 60s before SIGKILL, where systemd waited 90s.
  systemd sends SIGTERM to the broker alone and `reload` sends SIGHUP; the launchd broker
  logs through `--log` so SIGHUP reopens it. Reinstall printed units.
