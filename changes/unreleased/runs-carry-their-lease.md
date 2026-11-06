### Fixed

- **A run is charged to the lease its checkout binds.** The broker learned a run's lease
  only from `BAGGAGE`, so runs read `unleased` and `jobs.max_validations` never applied.
  A CLI run, `magus server status --wait` and an SDK `Open` now name the lease the checkout's
  marker binds.
