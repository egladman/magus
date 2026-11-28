### Changed

- **`magus vcs resolve` with nothing conflicted regenerates what the branch owes.** It
  reads the range from the base to the tip through the VCS driver, so git, Mercurial,
  Sapling and Jujutsu answer alike. It stages nothing: it names the review command and
  the fold. Nothing refuses an owed range yet.
- **Each driver reports its continue and fold commands.** The stale-output notice and
  the guard use them, so an hg or sl `rebase --continue` is judged and no notice names
  git outside git. jj reports that its operations never stop.
