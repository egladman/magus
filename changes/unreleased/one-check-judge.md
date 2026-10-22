### Fixed

- **A fork grades a job's check where its holder runs it**, so `magus describe job`
  and the guard agree on the check.
- **A check the guard could not grade refuses the fork** with one MGS3039 problem
  quoting why.
- **A `--checkout` fork may start from a sibling branch checkpoint** without MGS3041.
