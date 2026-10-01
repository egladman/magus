### Fixed

- **A worker can wait on a job it forked.** `magus job wait` and `magus\job.wait`
  refused every call from a checkout holding a lease, so a worker could never verify its
  own child. A holder now waits on any job below its lease. Its own job, a sibling's, an
  ancestor's and any unrelated job stay refused.
