### Fixed

- **The Claude Code mod's manifest passes `claude plugin validate` on older Claude Code.**
  `plugin.json` no longer carries `options` on its five string settings or the unknown
  `types` field, and names an author. The hooks already read an unrecognised value as
  the documented default, so a mistyped gate setting still never drops a guard.
