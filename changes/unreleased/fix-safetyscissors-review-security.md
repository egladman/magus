### Security

- **A lease's literal write path grants at most its immediate parent.** A path under a
  missing directory grants nothing, where it granted the nearest existing ancestor,
  which could be a whole top-level directory.
- **Breaking: a grant, run or grade line with an undeclared member is refused.**
  `grants.jsonl` and `grades.jsonl` fail with an `*UnknownMemberError`, as
  `attempts.jsonl` does, where an older build's compaction dropped the member and could
  widen a newer build's grant.
