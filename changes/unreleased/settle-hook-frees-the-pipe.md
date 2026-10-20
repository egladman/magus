### Fixed

- **A piped cherry-pick, merge or rebase returns once the settle hook exits.** The hook's
  saved copies of the original stdout and stderr were inherited by every process the run
  started, so a broker it started held the caller's pipe: `git cherry-pick X | tail`
  waited on the broker rather than on git.
