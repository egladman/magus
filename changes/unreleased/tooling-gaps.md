### Fixed

- **A checkpoint token now covers untracked files.** `magus vcs checkpoint -o name` and
  `magus job exec` used to name a tree holding only new files by the digest of an empty
  patch. The digest after the `+` now includes the untracked files, so a worker on the
  handed revision with new files reads as `revision-match` with its own digest. A tree with
  only tracked edits keeps the token it had.
- **`magus describe harness` prints what a current harness wires.** Text output lists each
  managed hook's matcher and command, and `-o json` carries them under `wired`, whether or
  not anything is left to merge.
- **The `proc\exec` warning about the magus binary fires only when a typed member answers
  the invocation, and names it** (`call magus\run instead`). `magus job exec`,
  `magus queue ls` and other invocations with no member no longer warn.
- **`magus\job.list` measures overlap footprints.** It left every footprint empty, while
  `magus ls jobs -o json` measured them. Both now measure them the same way.
