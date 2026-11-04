### Fixed

- **`magus doctor` no longer reports a working guard as unwired behind a tool-manager shim.**
  The harness probe keeps `HOME` and `XDG_CONFIG_HOME` and redirects only `XDG_STATE_HOME` and
  `TMPDIR`, so a `magus` that resolves through a mise shim still trusts its own config.
