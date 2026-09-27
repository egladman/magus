### Fixed

- **A worker can fork a child of its own job.** The guard refused every `magus job fork`
  and `op=fork` under a lease, though the job store grades exactly that. A row naming the
  caller's lease as `parent` now reaches the store, which refuses a child reaching past
  its parent's paths.
