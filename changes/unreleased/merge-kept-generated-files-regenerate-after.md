### Changed

- **A merge regenerates what it changed, in the merge.** The merge driver's registration
  writes settle hooks running `magus vcs resolve --hook` on the whole tree: projects
  whose sources or outputs the operation changed regenerate and are staged, a clean
  merge's commit is left to `git commit`, and a failed regeneration stops the commit.
  `regenerate-owed` is gone.
