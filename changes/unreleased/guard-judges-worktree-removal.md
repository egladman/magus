### Changed

- **The guard judges `git worktree remove` instead of refusing every one.** A
  removal passes when the path is a clean, unlocked linked worktree of this
  repository other than the session's own, no live job was taken in it, and
  every commit on it is on a remote-tracking ref or the base branch, or a
  finished job taken there filed its result. Otherwise the refusal names each
  failed condition and the command that inspects it. `--force` changes nothing,
  and a fact the guard cannot read refuses. `jj workspace forget` stays refused.

### Added

- **`types.CheckoutReporter`**, a VCS driver capability that lists a
  repository's registered checkouts with their revision and lock, and names the
  revisions no remote-tracking ref or base reaches. git implements it.
