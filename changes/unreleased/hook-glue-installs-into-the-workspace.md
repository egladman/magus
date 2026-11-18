### Fixed

- **Breaking: hooks run the glue `magus agent harness install` copies into the workspace.**
  The Claude Code, Codex and Cursor hook lines named `docs/guides/integrations/agents/`,
  which only a checkout of magus holds, so every hook in another workspace failed. Install
  now copies each script a hook runs into `.magus/harness/hooks/`, stamped with its glue
  version and digest, and the hook lines run it from there. `magus agent harness verify`
  and `magus doctor` name a missing copy, an edited one and one from another magus, each
  with the remedy `magus agent harness install`. Re-run it after upgrading: a line installed
  before this names the old path. Until then, a session still running an old line runs the
  glue magus embeds, and says so once per session. The guard templates move to version 39.
