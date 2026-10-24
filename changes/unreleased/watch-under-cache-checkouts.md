### Fixed

- **The file watcher reports changes in a checkout under a dot directory.** The built-in
  ignore rule judged every segment of the absolute path, so a tree under
  `~/.cache/magus/checkouts` or `.claude/worktrees` had every event dropped. It now judges
  only paths under the watched root.
