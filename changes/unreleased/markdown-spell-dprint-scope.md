### Changed

- **The markdown spell's `dprint` op stays in its own project.** It passes
  `--config-discovery=ignore-descendants`, so a run no longer also formats each nested
  `dprint.json`'s subtree. A folder whose `dprint.json` is not its own project now formats
  under its parent's config. The new argv costs every project one cache miss.
