### Added

- **A grouping lens reports what splitting work across workers cost in this repository.**
  Every worker loads a fixed context before its first edit, so grouping sequential work can
  beat splitting it, and the break-even differs per repository. A job outcome in the history
  file (version 8) now records its parent's attempt, the projects its write paths reach, the
  writes from outside it, and a work profile the guard's trail held: calls, calls and context
  before the first write, total and peak context, and denials. A trail that rotated past the
  attempt, or holds no call of it, records the profile as unknown, never as an idle holder.
  The lens sorts the outcomes into chained (one worker across two or more projects), split
  (siblings of one parent attempt) and solo, and reports per shape the setup context, context
  and wall time per group, and denials and unattributed writes per job. Fewer than 5 groups,
  or most trails unknown, answers `unknown` with the counts. It reports; a person writes any
  grouping rule. The magus-multi-agent skill reads it in place of its fixed coalesce rules,
  which remain the stated default where the lens answers unknown.
