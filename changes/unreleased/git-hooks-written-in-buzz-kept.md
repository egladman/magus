### Added

- **Git hooks written in Buzz, kept under version control.** `spells/git/hooks.buzz`
  installs a shim for each `<hook>.buzz` in a directory you name, honoring
  `core.hooksPath` and linked worktrees; `remove` deletes only its own. It writes only
  under `rw`, and a hook it did not write is an error.
