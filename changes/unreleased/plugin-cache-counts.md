### Changed

- **The plugin check reads only what Claude Code records.** A cached plugin with no
  `installed_plugins.json` entry never loads, so it now reads as `uninstalled`, not
  verified. `disabled` and `behind` (the recorded commit is not the marketplace clone's)
  are new. Each names its `claude plugin` fix, and doctor fails on all three. An
  unprovable currency says unknown.
