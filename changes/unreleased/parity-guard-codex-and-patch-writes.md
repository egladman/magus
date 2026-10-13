### Fixed

- **The guard judges Codex spawns and every host's patch writes.** It reads Codex's
  spawn and continuation shapes, its string spawn response and its transcript token
  usage, and takes the files an `apply_patch` adds, updates, deletes or moves as
  writes. A push that follows a commit on one line is refused, and a leased worker
  may `git init` outside its checkout.
