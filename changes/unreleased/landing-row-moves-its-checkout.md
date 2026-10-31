### Changed

- **A worker fast-forwards its own checkout from anywhere, and only there.** `git -C <checkout>
  merge --ff-only <commit id>` now passes for the checkout its lease was taken in, so a row on
  the landing worktree moves it without a person. The same call in any other checkout, on the
  base branch or naming a branch is refused, and a plain one is now placed like a commit.
