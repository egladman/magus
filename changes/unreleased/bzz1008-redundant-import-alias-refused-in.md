### Added

- **BZZ1008: a redundant import alias is refused in magusfiles and embedded Buzz.**
  `import "path" as alias;` errors when `alias` repeats the default binding, for
  `spells/`, `project/`, `magus/spell/<name>` and `buzz:` imports; a file import's
  alias isolates it, so it is exempt, as is `as _`. The root magusfile and built-in
  harness spells dropped their redundant `as codex`, `as cursor`, `as opencode`.
