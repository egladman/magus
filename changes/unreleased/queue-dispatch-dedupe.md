### Fixed

- **This repository's merge queue starts a validation run only when no unfinished one
  covers the intent.** The dispatch step reads queue.yaml's runs on main and skips when a
  run is pending or planned after the intent, so intents no longer cancel each other's
  pending runs. Each decision is a named notice.
