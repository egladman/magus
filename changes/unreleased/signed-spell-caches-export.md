### Added

- **Signed spell caches.** A spell's sandbox declaration names which locations
  are caches, e.g. the go spell's `GOCACHE`/`GOMODCACHE`. `magus config cache
  export --remote` signs each spell's caches into the remote tier, keeping only
  the entries a run used; `import --remote` restores the newest bundle that
  verifies against the trust set. An unverified bundle is refused.
