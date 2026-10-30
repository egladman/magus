### Changed

- **A wait denial serves the run it waited on.** `busy-wait` and `sleep-poll` answer a
  line waiting on one target run with that run under `magus job exec --timeout`, to run
  in the background. The bound is the target's last run here plus half, rounded up to a
  minute. With no run recorded, the denial says the bound is `background-unbounded`'s
  ceiling and no measurement.
