### Fixed

- **Agent-tool model aliases survive installed harness rows.** The model facts read by
  hooks retain each harness row's alias, so `opus`, `sonnet`, and `haiku` resolve
  through the existing model-tier checks. Unknown or unassigned aliases remain
  denied. Reinstall harness model facts with `magus agent harness install` after
  rebuilding to refresh existing installations.
