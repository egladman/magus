### Fixed

- **`merge-base-into-branches.buzz` rebuilds `./magus` after the merge commits.** The binary
  built before the merge can predate a `magus.yaml` key the merge brings, fail MGS1040 on
  every call, and leave the guard refusing the rebuild too. The script now rebuilds at the
  merged head before the push and stops, naming the failure, when that build fails.
  `rebase-branches-onto-base.buzz` already built after its rebase and is unchanged.
