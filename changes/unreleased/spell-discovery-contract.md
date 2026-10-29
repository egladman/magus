### Added

- **A spell can report the projects its markers form.** A spell exporting
  `discover_projects` answers one claim per directory: role, name, dependencies, spanned
  directories, gaps, and a map from lifecycle targets to its own ops. magus refuses `ci`,
  unknown targets, undeclared ops and unclean paths.
