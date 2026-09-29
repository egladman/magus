### Fixed

- **A target reached through `ctx.needs` runs at `--concurrency 1`.** A needed target
  whose body ran an install or a child magus queued for a slot behind the one its own
  pool worker held, so `magus --concurrency 1 run test` hung until `--target-timeout`.
  The body now yields that slot the way a scheduled step does. Needed targets and
  per-spell fan-outs now show on the MGS3013 deadlock check, so a pool they wedge is
  refused within seconds instead of hanging, and a yield nested inside another no
  longer ends the outer one early.
