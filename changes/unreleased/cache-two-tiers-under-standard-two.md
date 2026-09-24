### Added

- **The cache is two tiers under standard two-tier semantics.** Reads go local, then
  remote; a remote hit is verified and promoted into the local tier; a build is stored in
  both, each under its own gate. `cache.remote.write.enabled` gates the remote tier:
  unset, it is written when a signing key is held; `true` makes remote writes required.
