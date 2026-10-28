### Fixed

- **A run is charged to the lease its checkout binds.** The broker learned a run's lease
  only from `BAGGAGE`, which no process `magus job exec` binds carries, so every run read
  `unleased` in `magus status` and `jobs.max_validations` never applied. A CLI run, a
  `magus status --wait` and an SDK `Open` now name the lease the checkout's marker binds.
  Runs the daemon executes still share its one unleased connection.
