### Added

- **Discovery merges every spell's claims into one magusfile per project.** magus walks
  the tree once, hands each spell's `discover_projects` the files its globs match, and
  merges the answers per directory into `magusfile.buzz` source. A directory with a
  magusfile keeps it, and every rule and gap names what overrides or settles it.
