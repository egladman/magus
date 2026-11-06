### Changed

- **Breaking: `magus status` is `magus server status`, and `magus broker` is
  `magus server broker`.** Every flag carries over: `server status --wait | magus run`
  still waits for a seat, and `server broker status`, `stop` and `units` keep their
  flags. Neither old name has an alias. Regenerate supervisor units with
  `magus server broker units`: the old ones run `magus broker`.
- **`magus server status` reports a stopped server instead of failing.** It prints
  `server - not running` and the rest of the report, and exits 0; `--probe` is the
  check a script chains on. It starts nothing.
- The help group "Integrations and services" splits into "Background processes
  (start on demand)", holding `server` and `mcp`, and "Integrations".
