### Fixed

- **An agent bound by its session names its lease to rewrite through magus.** The shell
  hook refuses `refs --rename`, dry run included, and a `magus buzz` program that writes
  through `fs`, under rule `edit-ungraded`, until the line carries
  `export BAGGAGE=magus.lease=<id>;`. The process then grades each file against the job's
  write paths. A person's process stays unbound.
- **A Go program's guard judges the workspace it opened, not the process's directory.**
  A run or Buzz `fs` write in a workspace opened at a directory with no magus file read
  the target policies of whatever workspace the process started in. A slow policy read
  timed the run out, and a `human_only` declaration there refused it.
