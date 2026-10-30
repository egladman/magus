### Changed

- **Breaking: the daemon trusts no hosted origin by default.** `mcp.allowed_origins`
  lists the exact `scheme://host` origins past loopback that may call the console
  routes, and it is empty by default. `magus graph export --open` fails naming the key
  when the explorer's origin is not listed; `--print` and a loopback `--url` still work.
  The per-run loopback servers behind `--serve` and `run --open` now refuse a forged
  `Host` or a foreign `Origin`.
