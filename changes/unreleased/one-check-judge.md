### Fixed

- **A fork grades a job's check where its holder runs it.** A job forked with `--checkout`
  is judged in that checkout, so a check the fork accepts is the one `magus describe job`
  briefs and the guard lets the holder run; a `ci` check its holder gates in its own
  checkout is no longer refused by the fork or by `describe job`.
- **A check the guard could not grade refuses the fork.** When the workspace's rules do not
  load, or the guard denies every command, the fork reports one MGS3039 problem quoting why
  instead of a warning beside an accepted row.
- **A `--checkout` fork may start from any revision the repository holds.** A checkpoint on
  a sibling branch no longer reports MGS3041, and `--fix` moves a checkpoint to HEAD only
  when the repository does not hold its revision.
