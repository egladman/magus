### Changed

- **A job that filed its result no longer holds a run's outputs.** Only a declared or
  running job owns its write paths, for a write and for a run that regenerates an output in
  them alike, so an exited job waiting on `magus job wait` blocks nobody's run.
- **A rejecting `magus job wait` sends an exited job back to rework.** The job runs again
  and re-claims its write paths under the rule a claim meets. When another job took one
  meanwhile, the job stays exited, and the wait names that job.
- **A held path or output names who can release it.** A caller outside the holder's job
  tree is no longer told to run `magus job wait`, which it may not run. The refusal names
  the root of the holder's tree and files one attention row for it.
