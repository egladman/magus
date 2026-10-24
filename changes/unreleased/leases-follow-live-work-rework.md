### Changed

- **A rejecting `magus job wait` sends an exited job back to rework.** The job runs again
  and re-claims its write paths under the rule a claim meets. When another job took one
  meanwhile, the job stays exited, and the wait names that job.
