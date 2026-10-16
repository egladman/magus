### Fixed

- **A piped cherry-pick, merge or rebase returns once the settle hook exits.** The hook
  quiets its regeneration by pointing stdout and stderr at a file, and the copies it kept
  of the originals were inherited by every process the run started. A broker it started
  then held the caller's pipe for as long as it lived, so `git cherry-pick X | tail`
  waited on the broker rather than on git.
