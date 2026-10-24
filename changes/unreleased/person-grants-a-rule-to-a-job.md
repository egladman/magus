### Added

- **A person can grant one guard rule to one job.** `magus job grant <job> <rule> [--for
  <duration>]` lowers the rule to advice for that job's calls until the duration passes or
  the job ends, and `--revoke` ends it early. Only `hooks-off` is grantable. The new
  `grant-is-a-persons` rule refuses `magus job grant` to every agent, the root included.
