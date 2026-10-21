### Changed

- **A squash-merged `./magus` is no longer called foreign.** The off-base write rule
  judges a build whose commit is off the base branch by its stamped sources: when none
  differs from the base's, the build counts as on base.
- **The worktree bootstrap can build.** `--build-from <checkout>` runs the checkout's
  `go-build` and then places that binary; a session holding a job lease is refused.
- **Spawn refuses an integrator row bound to another checkout.** The refusal names where
  to spawn it, and the stale-binary, build and gate refusals each name a command the
  refused role can run.
