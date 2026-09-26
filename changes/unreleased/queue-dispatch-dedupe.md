### Fixed

- **This repository's merge queue starts a validation run only when no unfinished one
  covers the change.** On merge intent, and when CI finishes on a queued head, the queue
  reads queue.yaml's runs on main and skips when a run is pending or planned after that
  moment, so the triggers no longer cancel each other's pending runs. Each decision is a
  named notice.
