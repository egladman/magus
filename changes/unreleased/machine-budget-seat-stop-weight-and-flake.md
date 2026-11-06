### Fixed

- **A run under `magus job exec --timeout` gets no extra room on a full machine.** It
  shares the wrapper's slots only, and bad memory refuses it. `magus server broker stop` now
  stops a broker of another build, and a job's `weight` orders the requests the broker
  holds.
