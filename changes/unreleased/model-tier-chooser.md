### Added

- **`magus\modelTier` registers an optional function of the job's declared record and recorded history** that chooses a tier when the row names none. It and `magus\modelTiers` run when a job is forked, never during a run, and select nothing per call.
