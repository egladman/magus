### Changed

- **Breaking: a spell is checked against the spell contract on every load.** An `mgs_`
  export with another signature raises MGS1051, and each answer decodes strictly, naming
  the field path of a mismatch. `mgs_listTargets` returns only `{str: fun(Target) Command}`,
  services move to `mgs_listServices`, `mgs_listManifests` returns `[Manifest]`, and
  `mgs_listClaimedGlobs` is gone. Binding an unregistered spell is an error.
