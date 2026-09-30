### Fixed

- **The broker's crash reaper records every hosted service.** A service keyed by its
  workspace path was never written to the journal, so a new broker had nothing to stop.
