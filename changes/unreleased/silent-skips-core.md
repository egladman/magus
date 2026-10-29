### Changed

- **A check that did not run says so, even under `-s` and `log.silent`.** A record marked
  `unchecked` passes a quiet display at every level. The sandbox running with the workspace
  write grant on an unreadable job store, a host without landlock, an unreadable
  `jobs.stale_after`, a bound spell exposing no targets, and a job row written with no tier
  or an ungraded check now print through silent mode.
- **The guard says when a lease check fails open.** A spawn whose lease store does not read
  is advised with the store's error, and a write whose workspace cache does not resolve is
  advised that no lease boundary was checked.
- **A workspace shell deny rule holds on a line that does not parse.** A line the guard
  cannot parse is refused when any word in it is a deny rule's program. It used to pass.
- **Doctor counts a check that could not look as unknown.** `undeclared-seeding-files` with
  no VCS or no tracked-file listing, and `self-staling-outputs` with no VCS or a project whose
  tracked outputs would not list, render `[unknown]` and count in the summary's unknown tally
  rather than as a pass.
