### Changed

- **A stale `./magus` lets the steps that clear it through.** Finishing a series of picks
  (`git cherry-pick <commit>...`, `--skip`) and writes outside any workspace, such as memory
  or a scratchpad, now pass while the tree does not load.
