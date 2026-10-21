### Changed

- **Memory claims pool runs that differ only in args or in charms the target never
  reads.** A measured claim counts every run sharing the charms its `ctx.needs` chain
  reads, so its history reaches the three runs a claim trusts sooner. A run with
  forwarded args is sized from other such runs, never above the full-run peak.
