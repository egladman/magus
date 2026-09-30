### Changed

- **Breaking: a `../` spell import never leaves the workspace.** The spell search joins
  an import onto each level from the workspace root down to the magusfile, and a `../`
  joined onto a shallow level used to reach past the root, so a worktree could load the
  main checkout's spell. A candidate outside the root is now skipped unprobed, and an
  import with no candidate inside the root fails with MGS1047 naming the import, the
  level and the root.
