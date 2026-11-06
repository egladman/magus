### Changed

- **Breaking: `magus status` is `magus server status`, and `magus broker` is
  `magus server broker`.** Every flag carries over, and `server broker status`,
  `stop` and `units` keep theirs. Neither old name has an alias. Regenerate supervisor units with
  `magus server broker units`: the old ones run `magus broker`.
- **`magus server status` reports a stopped server instead of failing.** It prints
  `server - not running` and the rest of the report, and exits 0; `--probe` is the
  check a script chains on. It starts nothing.
- **`magus -h` lists `server` and `mcp` under "Background processes (start on
  demand)".** The rest of the old "Integrations and services" group is now
  "Integrations".
