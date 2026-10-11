### Changed

- **A guard check answers within 7s instead of being cut off.** Workspace rules share
  what remains; when time runs out a write, spawn or state-changing command is denied
  and a read goes through with a note naming the slow phase. A dry run has no deadline.
  The next check reports ones the host abandoned (`guard-unfinished`); trail rows
  record phase timings.
