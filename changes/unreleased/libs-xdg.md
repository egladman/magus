### Changed

- **With no home directory, magus says so instead of keeping history under a relative
  path.** The default `history_path` fails config loading with an error naming
  `XDG_STATE_HOME`, and the REPL runs without recall after saying why. Both resolve
  through the new `libs/xdg` module, which ignores a relative `XDG_STATE_HOME`.
