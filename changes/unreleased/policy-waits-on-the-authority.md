### Changed

- **Breaking: a worker's committed loosening waits on its authority.** A leased call's
  policy loads from its row's checkout, and its approved rules are read at the row's
  `authority`, not at `HEAD`. Every path changed since then is pending, commits
  included. A call with no lease keeps its checkout's committed revision. Every door
  resolves the lease before the policy loads.
- **Each VCS backend lists the paths changed since a revision.** git, Mercurial, Sapling
  and jj implement it. A driver without it leaves the authority unreadable, and the
  approved side's failure says so.
- **Breaking: `JudgeCommand` and `JudgeWrite` judge with the approved rules when the
  working tree's do not load**, as the hook does, and report the failure on the
  judgment. They return an error only when no Buzz engine is linked.
- **A caller with no job row names its authority.** `magus.ContextWithAuthority` carries a
  revision, such as a pull request's target branch, so the change cannot approve its own
  loosening.
  [ADR 0045](https://eli.gladman.cc/magus/decisions/0045-a-worker-is-judged-by-its-checkpoint/).
