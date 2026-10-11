### Changed

- **A guard check answers within 7s instead of being cut off.** Workspace rules share
  what remains; when time runs out a write, spawn or state-changing command is denied
  and a read goes through with a note naming the slow phase. The next check reports
  earlier ones the host abandoned, and trail rows record phase timings.
