### Changed

- **`magus server status` reports a stopped server instead of failing.** It prints
  `server - not running` and the rest of the report, and exits 0; `--probe` is the
  check a script chains on. It starts nothing.
