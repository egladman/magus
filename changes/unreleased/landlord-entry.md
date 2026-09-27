### Added

- **Enter a live job's path instead of spawning a worker for one small change.**
  `magus_job op=fork id=<job> enter=<path>`, or a `magus job fork --stdin` record
  carrying `enter`, records the entry on the job; the guard then lets one write
  into that path through once the job's holder has been idle for a minute, and
  stamps the entry consumed. A job takes two. `describe job` and `job wait` list
  entries, and the holder is advised to re-read an entered path. The job schema
  stays at version 11: an older magus keeps a row's entries when it rewrites the row.

### Fixed

- **A catch-all lease no longer turns every word of a shell line into a path.**
  Under a `**` declaration only a redirect target or a file-shaped word is graded,
  so `python3 -c 'print(1)'` and `echo hi` pass beside it.
