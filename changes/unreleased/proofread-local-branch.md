### Added

- **`local-branch`, a proofread house rule.** It reports a branch that exists only on the
  author's machine in a pull request, commit, issue, changelog or review. Proofread cannot
  tell a local branch from text, so `--local-branches FILE` lists the names; with none, it
  judges nothing. A one-word name counts only in backticks. It is off by default, and this
  repository denies it, listing every local branch no remote holds.
