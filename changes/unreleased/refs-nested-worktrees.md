### Fixed

- **Symbol lookups finish in workspaces with nested checkouts.** `magus refs`
  skips linked checkouts when checking a missing symbol against workspace text.
  Default text searches classify only files with matches, reducing work in large
  repositories.
