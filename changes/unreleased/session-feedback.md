### Added

- **`magus\trail` reads what the guard did in a session.** `magus\trail.read` returns one
  session's guard record from every checkout's trail: each call with its verdict, rule,
  served nexts and command shape, plus its subagents. `.shape` and `.shapes` normalize a
  shell line so calls differing only in paths and literals read alike. `.mark` and
  `.marks` keep a person's verdicts on report rows.
