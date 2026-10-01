### Changed

- **A worker may commit in its own checkout.** `lease-vcs` now lets a worker lease
  commit, with any backend, on its own branch in the checkout its lease was taken in
  (`checkout_root`). It still refuses push, stash, revert, reset, clean, rebase, merge,
  cherry-pick, `checkout .`/`restore .`, worktree removal, and a commit anywhere else:
  another tree, the primary checkout, the base branch, or a detached HEAD. `git commit`,
  `git -C <dir> commit`, `cd <dir> && git commit`, `GIT_DIR`/`--git-dir`, and `vcs.cmd`
  in a Buzz script all get the same answer.
- **Every guard deny cites its verdict ref.** A deny's first firing now stores the full
  verdict and prints `full verdict: magus query output grd...` the way a repeat does.

### Fixed

- **`lease-vcs` graded a parentless row as the orchestrator's.** A worker forked without
  `--parent` could push. A lease now counts as a worker when its row has a parent, when
  a subagent holds it, or when its caller names no session. Only a root session holding
  a parentless row is the root.
- **`vcs.cmd` ran version control unguarded under a lease.** It now asks `lease-vcs`
  before it runs, and refuses outright when a lease is acting and no guard is linked in.
