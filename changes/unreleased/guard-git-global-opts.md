### Fixed

- **Global options no longer hide a VCS command from the guard.** `git -C . reset
  --hard`, `git --no-pager stash`, `hg -R . purge` and `jj --at-op @ abandon` reach the
  rule their subcommand triggers, and a push is still graded in the checkout `-C` names.
  An inline alias (`git -c alias.x=...`, `hg --config alias.x=...`) is refused under the
  new `inline-alias` rule.
