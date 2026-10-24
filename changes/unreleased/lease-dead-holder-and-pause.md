### Added

- **A dead holder's path goes to the job that needs it.** When a write lands in a path
  whose holder's session ended, or went silent past 30 minutes with no background command
  running, magus claims that write path for the writer with a `dead:<job>` proof. Both
  rows record it: the claim names who held the path and why it was judged dead, and the
  holder's release names who took it.
- **`magus job pause` takes `--reason` and `--until`.** A pause records who paused the
  job and why, and lapses at its bound. While it holds, the sweep and `magus job prune
  --all` leave the row alone.

### Changed

- **The session heartbeat store stays bounded.** Past 1024 sessions and leases it is
  rewritten to the newest, dropping stamps older than a week.
