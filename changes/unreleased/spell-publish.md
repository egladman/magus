### Added

- **Every spell magus ships is published with each release**, one repository per spell
  under `ghcr.io/egladman/magus/spells`, tagged with the release version: built-ins,
  source-only spells, and the harness and provider spells, which now ship in the binary
  as source only and are named by their directory (`harness/cursor`, `github/actions`).
  The digest `magus spell pull magus/spell/<name>` stamps on a copy is the one published.
- `magus spell ls magus/spell` lists every spell magus ships, each pinned to the digest a
  release publishes, with no network.
- `magus spell push magus/spell/<name> <ref>` pushes a spell magus ships, packed from the
  binary.
