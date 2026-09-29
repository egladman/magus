### Fixed

- **A hand-maintained file among generated ones merges as text again.** `.gitattributes`
  lists each declared output glob as written, then names every tracked file an output's
  exclusion carves out with `!merge !linguist-generated`, so git's own merge handles it
  and review shows its diff. The exclusions were written as `-merge` lines ahead of the
  globs they narrowed, so the glob won and the file went to the magus driver, and
  `-merge` would have made git treat the file as binary. A slashless output glob is now
  anchored at the root, as git matches one at every depth.
