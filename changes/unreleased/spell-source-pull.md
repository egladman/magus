### Added

- **`magus spell pull magus/spell/<name> <dir>` copies out a spell magus ships.** It
  writes the spell's published files from the binary with no network, stamps
  `spell.buzz` with the artifact's pinned reference, and prints the `magus.yaml` override
  to add. The new `spell-overrides` doctor check advises when the built-in has changed
  since the copy was pulled.
