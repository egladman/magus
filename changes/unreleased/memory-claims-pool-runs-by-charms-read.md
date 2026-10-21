### Changed

- **Memory claims pool runs that differ only in args or in charms the target never
  reads.** A target's measured claim now counts every run that shares the charms its
  `ctx.needs` chain reads and whether args were forwarded, so its history reaches the
  three runs a claim trusts sooner. A run with forwarded args is sized from other such
  runs, never above the full-run peak, and never sizes a full run.
