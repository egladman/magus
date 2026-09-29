### Changed

- **Built-in spells ship as their Buzz source, not bytecode.** The binary embeds each
  `spells/<dir>` directory and compiles the built-ins at startup, in a session that offers
  only `magus/spell`, `magus/charm` and `magus/lint`. A spell importing anything else fails
  there with BZZ2001 and ships as source only: `endoflife-date`, `onepassword` and
  `system-keychain`. The eleven committed `.bo` files and the `magus-utils spells`
  generator are gone. Loading the registry costs about 6.8ms per process, up from about
  2ms.

### Added

- **`magus spell pull magus/spell/<name> <dir>` copies out a spell magus ships.** It
  writes the files the spell's published artifact
  (`ghcr.io/egladman/magus/spells/<name>`) holds, from the binary with no network,
  stamps `spell.buzz` with that artifact's pinned reference, prints what a registry pull
  prints, and prints the `magus.yaml` override to add. A new `magus doctor` check,
  `spell-overrides`, advises when the built-in has changed since the copy was pulled.
