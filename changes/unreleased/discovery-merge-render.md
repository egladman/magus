### Added

- **Discovery merges every spell's claims into one magusfile per project.** magus walks
  the tree once and hands each spell that exports `discover_projects` the files its globs
  match, then merges the answers per directory: several spells claiming one directory
  make one project with each of them bound, a `member` claim joins the project that owns
  its path, and a directory a spell spans becomes a `depends_on` on the project nested
  there. Same-named targets run in spell-name order, and `ci` needs whichever of `lint`,
  `build` and `test` exist. Each project renders as `magusfile.buzz` source for
  `magus init` to write; a directory that already has a magusfile keeps it. Every rule
  discovery applied is reported with the line that overrides it, and every gap names the
  file and the setting that settles it. `magus init` does not call it yet.
