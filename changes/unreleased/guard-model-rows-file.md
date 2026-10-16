### Changed

- **The guard hook reads its model rows and tiers from the tool rows directory.**
  `magus agent harness install` writes the harness's model rows and the workspace's model
  tiers beside the tool rows, as `models` and `tiers`, and a hook judgment reads them
  instead of loading the whole workspace, which took most of a judgment and, under several
  workers, more than the hook's deadline. Rerun `magus agent harness install` to write
  them; until then the guard loads the workspace and says why.
