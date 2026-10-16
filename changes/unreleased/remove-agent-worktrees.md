### Fixed

- **An ended job's linked git worktree is removed even when magus did not create it**, such as an agent host's isolation worktree, once removing it loses nothing: no changed or untracked file, and a reachable HEAD. A dirty one names its files, one with unreachable commits names the newest, and the main checkout is never removed.
