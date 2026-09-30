### Changed

- **Built-in spells ship as their Buzz source, not bytecode.** The binary embeds each
  `spells/<dir>` and compiles the built-ins at startup, offering only `magus/spell`,
  `magus/charm` and `magus/lint`. A spell importing anything else ships as source only:
  `endoflife-date`, `onepassword` and `system-keychain`. The committed `.bo` files and
  the `magus-utils spells` generator are gone.
