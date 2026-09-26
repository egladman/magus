### Fixed

- **Global options no longer hide a VCS command from the guard.** `git -C . reset --hard`,
  `hg -R . purge` and `jj --at-op @ abandon` reach the rule their subcommand triggers.
  An inline alias such as `git -c alias.x=...` is refused under the new `inline-alias`
  rule.
