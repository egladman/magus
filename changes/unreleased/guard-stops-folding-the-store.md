### Fixed

- **A leased guard call no longer reads the whole session store.** A release request
  now also sits on the holder's job row, where the guard reads it. The advice fires once
  per lease, so one worker's notice no longer silences another's in the same session.
- **A slow guard grade no longer reads as a denied check.** `job fork --dry-run` gives
  the workspace open its full two minutes, and a judgment that runs out of time defers
  the check's grade instead of reporting MGS3040. A real deny still refuses.
- **The trail says where a guard judgment's time went.** Rows time `builtin_rules` and
  `notices`, a lapsed row keeps a bound worker's lease, and the stall note names the
  stalled phase rather than blaming load or a daemon from another build.
