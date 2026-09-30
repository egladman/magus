### Changed

- **Breaking: an import never leaves the workspace.** A `../` spell or module import
  joined onto a shallow search level used to reach past the root, so a worktree could
  load a sibling checkout's file. A candidate outside the root is now skipped, and an
  import with none inside it fails with MGS1047 naming the import, level and root.
