### Changed

- **`lease-vcs` still refuses push, stash, revert, reset, clean, rebase and merge.** It also refuses `checkout .`/`restore .`, worktree removal, and a commit in another tree, the primary checkout, the base branch or a detached HEAD.
