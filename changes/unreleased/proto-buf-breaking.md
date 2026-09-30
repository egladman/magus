### Changed

- **The `buf-breaking` op takes its baseline from the caller.** It no longer compares
  against `.git#branch=main`, so a repo on another default branch, or a module below the
  git root, passes `--against` with the ref and `subdir=`. The proto project's `ci` now
  runs it against the default branch `vcs` resolves.
