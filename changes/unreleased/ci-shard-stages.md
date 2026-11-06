### Fixed

- **A CI shard runs only its own units.** `magus run --stdin` refuses a plan of more than one
  shard when no `--shard` names one (MGS3029), and `--shard all` runs every shard. The shard
  job passes `--shard` on argv, since the sandboxed gate scrubs `MAGUS_SHARD` from the run it
  starts, and every shard had been running the whole plan.
