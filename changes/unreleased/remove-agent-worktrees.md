### Fixed

- **An ended job's linked git worktree is removed even when magus did not create it**, such as an agent host's isolation worktree, once removing it loses nothing: no changed or untracked file, and a HEAD a branch, tag or remote-tracking ref reaches. A dirty one names its files, one with unreachable commits names the newest, and the main checkout is never removed. Before, `magus job exit` kept every such worktree with "is not a checkout CreateCheckout made".
