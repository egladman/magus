### Fixed

- **A worker can wait the way the guard tells it to.** `magus status --wait` piped into
  any magus call, `graph build` included, or followed by `&&` and the call, passes
  sleep-poll. A subagent may stop while a run under `magus job exec --timeout` or a commit
  it backgrounded is still going: its completion notice is the wait. check-timeout refuses
  once per target per session, so a narrowed rerun goes through.
- **A worker runs any target read-only over its own paths.** A run with
  `--no-default-charms` and no rw charm, over projects holding the row's write paths,
  passes worker-check-only, so tests written under a lint check get run.
- **Guard verdict refs resolve from the worker's checkout.** A verdict stored from a linked
  worktree is stored in the primary checkout too, where `magus query output` looks.
- **`git reset --hard <rev>` on a clean tree no live job holds is judged as `git switch`.**
