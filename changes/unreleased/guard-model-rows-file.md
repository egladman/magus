### Changed

- **The guard hook reads its model rows and tiers from the tool rows directory.**
  `magus agent harness install` writes them beside the tool rows as `models` and `tiers`,
  so a hook judgment no longer loads the whole workspace, which under several workers
  overran the hook's deadline. Until the install is rerun, the guard loads the workspace
  and says why.
