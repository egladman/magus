### Added

- **A spell can report the projects its markers form.** A spell that exports
  `discover_projects(target: Target, req: DiscoveryQuery) > [DiscoveredProject]` answers
  with one claim per directory: its role (`project`, or `member` of the owning project),
  name, dependencies, the directories its tool spans, the gaps it could not wire, and a
  `targets` map from `install`, `generate`, `format`, `lint`, `build`, `test` or
  `security` to its own op keys. magus refuses an answer that reports `ci`, a target name
  outside that list, an op the spell does not declare, or a path that is not clean and
  workspace-relative. `magus init` is the caller; no spell implements it yet.
