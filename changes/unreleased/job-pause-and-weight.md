### Added

- **`magus job pause` and `magus job resume` get one job out of a bind.** While a job is
  paused, `magus job exec` and the guard refuse its holder's next magus run with one line
  naming `magus job resume <job>`. A run already holding the machine budget finishes:
  nothing is preempted.
- **A job record takes an optional `weight` in (0,1], 0.5 when omitted.** A run bound to
  the job polls the machine budget every interval divided by weight/0.5, so a heavier
  job's waiters ask again sooner. It is a lean, never a guarantee. A weight out of range
  is refused at fork and apply.
