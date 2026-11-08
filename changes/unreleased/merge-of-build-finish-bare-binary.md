### Fixed

- **A merge of the build commit finishes only through PATH's `git` and this checkout's
  `magus`.** `./git add` or `elsewhere/magus vcs resolve` matched by base name before, and
  passed the stale-binary refusal while the merge was in progress.
