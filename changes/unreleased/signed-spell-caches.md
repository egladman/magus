### Added

- **Signed spell caches.** A spell's sandbox declaration names which of its locations
  are caches; the go spell names `GOCACHE` and `GOMODCACHE`. `magus config cache export
  --remote` signs each spell's caches into the remote tier, keeping only the entries a
  run used; `import --remote` restores the newest bundle that verifies against the trust
  set, so a magus miss rebuilds only what changed. An unverified bundle is refused.
- **`magus queue gate --cache` keeps the spells' caches beside the local tier**, each in
  the mode its spell grants it, so a restore and a save in boxes of the same `--cache`
  reach what the gate built with. `validate` keeps them in the candidate's box.

### Removed

- **`magus config cache export --toolchain` and `--used-within`, and `import
  --toolchain`.** `--remote` carries every cache the workspace's spells declare, and a
  save keeps only what was used since the last restore, with no flag to say so.
