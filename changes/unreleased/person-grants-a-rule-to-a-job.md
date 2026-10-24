### Added

- **A person can grant one guard rule to one job.** `magus job grant <job> <rule> [--for
  <duration>]` lowers the rule to advice for that job's calls alone, until the duration
  passes or the job ends, and `--revoke` ends it early. The advice names who typed the
  grant, when, and until when. Grants are lines in `grants.jsonl` beside the job store.
  Only `hooks-off` is grantable; any other rule is refused, naming the grantable ones.
- **The new `grant-is-a-persons` rule refuses `magus job grant` to every agent**, the
  root included, and the job store refuses a grant from a process carrying a lease. No
  MCP tool exposes it. The lease-negotiation proposal that used `magus job grant` for the
  root answering claim requests needs another word.
