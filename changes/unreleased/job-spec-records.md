### Changed

- **Breaking: the spawn-time rules are renamed.** `brief-command` is now `spawn-command`,
  `brief-plain-commands` is `spawn-plain-commands`, and `spawn-unbriefed`,
  `buzz-unbriefed` and `architecture-unbriefed` are `spawn-before-skill`,
  `buzz-before-skill` and `architecture-before-skill`. A `magus\guard.builtins` that
  still names an old one fails to load, naming the new one.
- **`magus job exec` ends by saying to read the spec** (`magus describe job <id>`) rather
  than the terms.
