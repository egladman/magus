### Changed

- **The agent guard hook reads the `human_only` targets from the knowledge store.** A `magus run` or `magus affected` line no longer opens the workspace to learn them while the store's record matches the tree; any record it cannot prove current is ignored and the workspace is opened as before.
