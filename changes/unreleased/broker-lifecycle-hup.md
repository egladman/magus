### Changed

- **SIGHUP no longer stops the server or the broker.** The server reloads its
  configuration, as `magus server reload` does; the broker reopens the file its new
  `--log` flag names, so a log rotator can move it aside. A broker a run starts logs
  through `--log` to `$XDG_STATE_HOME/magus/broker.log`.
