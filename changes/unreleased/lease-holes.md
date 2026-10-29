### Fixed

- **A worker's writes stay graded after `job exit`.** Exit keeps the binding, so a write
  outside the job's paths is denied. An unbound subagent's write into a job's checkout is
  denied, a lapsed guard no longer degrades it, and continuing an exited worker advises the
  root.
