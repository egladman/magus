### Fixed

- **A target reached through `ctx.needs` runs at `--concurrency 1`.** A needed target
  that ran an install or a child magus waited for the slot its own pool worker held, so
  `magus --concurrency 1 run test` hung until `--target-timeout`. Needed targets and
  per-spell fan-outs now show on the MGS3013 deadlock check, so a pool they wedge is
  refused within seconds.
