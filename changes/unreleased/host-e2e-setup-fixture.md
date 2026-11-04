### Fixed

- **The host end-to-end suite gets past its setup.** The fixture probed the guard under a
  harness name it had not installed, which `magus shell` refuses; it now probes as the
  host under test.
