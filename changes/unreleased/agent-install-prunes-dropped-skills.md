### Changed

- **Breaking: `magus agent install` prunes the skills the catalog dropped; `--prune` is
  removed.** A stamped skill directory that is no longer shipped is deleted after the
  install writes, and each removal prints on stdout. An unstamped skill is never touched.
  `--dry-run` lists what would go, and `magus agent harness install` prints its removals the
  same way.
