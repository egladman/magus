### Changed

- **A re-fork keeps its checkpoint over the job's own commits.** `job fork` into a
  checkout holding the job's commits no longer moves the checkpoint to its head, so
  `job wait` grades those commits as the job's work. `job exec` there reads them as a
  base match.
- **`job wait` passes a read-only job with no check on its filed result.** The risks the
  result names are carried on the status.
- **A changed file another verified run read passes a file-scoped check.** `job wait`
  no longer refuses the check for leaving it out.
- **`job apply` names the filed runs its spec change sets aside.**
- **The narrowing hint prints one `--release-write-path` per path.**
- **A checkout whose row exited with no binary digest is no longer held.**
- **A finished branch behind its own upstream reads as integrated** once a base holds
  that upstream.
