### Added

- **`magus job pause` and `resume` get one job out of a bind, and a job takes a `weight`.**
  While paused, `job exec` and the guard refuse its holder's next run; a run holding the
  budget finishes. A `weight` in (0,1], 0.5 when omitted, makes a heavier job's waiters
  poll the budget sooner.
