### Added

- **`magus session --brief` restores a session's state after history loss.** Branch,
  unpushed commits, classified dirty tree, live leases and last failing targets, also as
  `-o json`. The `magus-rehydrate.sh` template wires it to session start.
