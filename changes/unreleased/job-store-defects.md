### Changed

- **`magus job exec` refuses a job another checkout holds.** It used to move the job's
  checkout silently, so `job wait` graded the second taker's tree instead of the
  holder's. The refusal names the holder's checkout, its state, how long ago it last
  moved, and the way to hand the job over: its holder runs `magus job exit`, then
  whoever forked it declares it again with `magus job apply`. Declaring an ended job
  again clears the checkout it was taken in. Running exec again in the same checkout
  still records the new base.
- **`magus job exec` starts a declared job running.** `magus ls jobs` shows `running`
  for a job somebody took, and HOLDER names the worktree it was taken in (`-` until
  somebody takes it, `server` for magus's own jobs) instead of `session` on every row.
  `-o json` keeps `holder` as before.
- **The leased-path advisory names whoever holds the path.** It says where the job
  was taken, or that nobody has taken it yet, instead of warning about "a concurrent
  agent".

### Fixed

- **`magus job wait` accepts a run recorded in the same second as the job was
  declared.** The declaration time is stored in whole seconds, and the comparison
  rounded it up to the end of its second, so a check run straight after `job fork` or
  `job apply` was rejected as older than the job.
