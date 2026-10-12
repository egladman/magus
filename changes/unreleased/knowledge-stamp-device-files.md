### Fixed

- **A device or pipe among the graph's inputs no longer invalidates it.** A git config
  with `core.excludesFile=/dev/null` made the domain class rebuild on every read, since
  any write to `/dev/null` moves its mtime. A device, named pipe or socket now counts by
  its kind alone, and the stamp never opens one, so a pipe cannot block it.
