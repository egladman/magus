### Fixed

- **A checkpoint token now covers untracked files.** `magus vcs checkpoint -o name` and
  `magus job exec` used to name a tree holding only new files by the digest of an empty
  patch. The digest after the `+` now includes untracked files, so a worker on the handed
  revision reads as `revision-match`. A tree with only tracked edits keeps its token.
