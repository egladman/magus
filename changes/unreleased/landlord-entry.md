### Added

- **Enter a live job's path instead of spawning a worker for one small change.**
  `magus job fork --stdin` with `enter`, or `magus_job op=fork enter=<path>`, records
  the entry. Once the holder has idled a minute, the guard lets one write into that
  path and marks the entry consumed. A job takes two; `describe job` and `job wait`
  list them.
