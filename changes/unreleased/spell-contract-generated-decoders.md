### Changed

- **A spell is checked against the spell contract on every load.** magus raises MGS1051
  for any `mgs_` export whose signature differs, then decodes each answer strictly: a
  wrong kind, an unknown member or a bad enum case names its field path. Breaking for
  spells written against the old leniency: `mgs_listTargets` returns
  `{str: fun(Target) Command}` only, so a record-shaped op, a mixed `> any` map or a
  handler of another signature no longer loads; services move to `mgs_listServices`;
  `mgs_listManifests` returns `[Manifest]`, not `[Path]`; `mgs_listClaimedGlobs` is gone.
  The typescript, podman and nx spells follow suit.
- **A workspace-local spell's import handle carries only its name, globs and ops.**
  Binding a spell that is not registered is an error naming the spell.
