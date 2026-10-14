### Added

- **Insight reports what each model tier achieved.** When a job reaches pass, fail or
  no_return the job store records its outcome in the history file (`history_path`), and the
  new model-tiers lens, in `magus\insight()` and the console's Insight section, folds those
  into one row per tier and job kind: pass rate with its Wilson lower bound, checks per job,
  re-forks of one id, rework rounds, widenings and cost. A rework round is a message sent to
  a worker after it handed back; a widening is a `job apply` that adds write paths to a job
  already taken. `magus doctor` gains `model-tier-fit`, which names the declaration, matrix
  or fork that routed jobs to a tier that passed too rarely. Nothing changes a tier on its
  own: the lens and the check report, and a person edits the line. A dry run records nothing.
  The history file's version is now 7, which an older magus refuses to read.
