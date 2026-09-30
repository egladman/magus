### Added

- **Every spell magus ships is published with each release.** Each gets one repository
  under `ghcr.io/egladman/magus/spells`, tagged with the release version. Harness and
  provider spells now ship in the binary as source only, named by their directory
  (`harness/cursor`, `github/actions`). The digest `magus spell pull magus/spell/<name>`
  stamps on a copy is the one published.
