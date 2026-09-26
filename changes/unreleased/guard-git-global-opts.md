### Fixed

- **git's global options no longer hide a command from the guard.** `git -C . reset
  --hard`, `git --no-pager stash` and `git -P push` reach the rule their subcommand
  triggers, and a push is still graded in the checkout its `-C` names. An alias defined
  inline (`git -c alias.x=... x`) is refused under the new `inline-alias` rule.
