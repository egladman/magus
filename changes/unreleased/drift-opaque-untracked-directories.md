### Fixed

- **Whole-tree generation no longer traverses pre-existing untracked directories.** The
  drift gate now treats VCS-collapsed directories as opaque, so a local `node_modules/`
  tree cannot exhaust the host's file descriptors while unrelated generators run. New or
  removed directories are still reported as generated-file drift.
