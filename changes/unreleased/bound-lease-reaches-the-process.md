### Fixed

- **An agent bound by its session names its lease to rewrite through magus.** The shell
  hook refuses `refs --rename`, dry run included, and a `magus buzz` program that writes
  through `fs`, under rule `edit-ungraded`, until the line carries
  `export BAGGAGE=magus.lease=<id>;`. The process then grades each file against the job's
  write paths. A person's process stays unbound.
