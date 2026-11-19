### Fixed

- **Breaking: hooks run the glue `magus agent harness install` copies into the workspace.**
  Hook lines named `docs/guides/integrations/agents/`, which only a magus checkout holds,
  so hooks failed everywhere else. Install now copies each hook script into
  `.magus/harness/hooks/`. `magus agent harness verify` and `magus doctor` name a missing
  or edited copy; re-run `magus agent harness install` after upgrading.
