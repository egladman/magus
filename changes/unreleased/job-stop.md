### Added

- **`magus job stop <job>` stops the process groups the broker recorded for a job.** It
  sends SIGTERM, then SIGKILL after a grace, and releases the claims and validation seats
  those groups held. It refuses a job with no recorded group, and signals nothing when a
  group's id was reused or its leader exited while the group runs on.
- **`magus server status` names each validation seat's holder:** the job, its pid, how
  long it has held the seat, and each recorded group and whether it is alive. A seat a
  dead group holds says so and names the `magus job stop` that frees it.
- **The process-signal guard rule names `magus job stop <job>`** when the process a
  `kill` targets is in a group the broker recorded for a job.
- **`magus server stop --pools` names each validation seat a dead group holds** and the
  `magus job stop` that frees it. It frees none itself.
