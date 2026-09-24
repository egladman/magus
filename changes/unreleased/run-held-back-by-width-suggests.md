### Added

- **A run held back by its width suggests `concurrency_profile: aggressive`.** It fires
  once per session, only after 15s or more queued for slots with cores idle. It stays
  silent under an explicit `concurrency`, an already aggressive profile, or a wait on the
  machine budget. Uptake is counted by `magus session hints` as `concurrency-profile`.
