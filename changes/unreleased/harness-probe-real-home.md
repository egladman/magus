### Fixed

- **`magus doctor` no longer reports a working guard as unwired behind a tool-manager shim.**
  The harness probe keeps `HOME` and redirects only the XDG state and config directories and
  `TMPDIR`, so a `magus` that resolves through a shim still finds its own records.
