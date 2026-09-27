### Fixed

- **A worker can wait on a job it forked.** `magus job wait` and `magus_job` `op=wait`
  refused every call from a checkout holding a lease, so a worker that forked a child
  could never verify it or record its pass. A holder now waits on any job below its own
  lease, a child or a grandchild. Its own job, a sibling's, an ancestor's and any
  unrelated job stay refused.
