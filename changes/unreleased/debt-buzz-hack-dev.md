### Changed

- **The `hack/dev` scripts return errors as values.** The worktree lister, harness sync,
  the rename scripts, the module-call rewriter, the rebase script and the checkout
  bootstrap's helpers return a `Result`; their no-throw baseline rows fall. `placeBinary`
  and `verifyReady` still raise while `hack/magusfile` calls them.
