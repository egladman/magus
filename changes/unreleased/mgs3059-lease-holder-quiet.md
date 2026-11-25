### Added

- **MGS3059 names a quiet lease holder.** A path another job holds, whose row nobody
  has touched within `jobs.stale_after`, keeps its refusal in `magus describe job` and
  now names the holder's idle time and the `magus job prune --dry-run --all <job>` line
  the root or a person checks it with.
