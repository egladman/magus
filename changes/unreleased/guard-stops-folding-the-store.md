### Fixed

- **A slow guard grade no longer reads as a denied check.** `job fork --dry-run` gives
  the workspace open its full two minutes, and a judgment that runs out of time defers
  the check's grade instead of reporting MGS3040. A real deny still refuses.
- **The trail says where a guard judgment's time went.** Rows time `builtin_rules` and
  `notices`, a lapsed row keeps a bound worker's lease, and the stall note names the
  stalled phase rather than blaming load or a daemon from another build.
