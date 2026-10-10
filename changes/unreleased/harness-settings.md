### Added

- **An opt-in `claude-code-mod` harness that turns the magus Claude Code mod on.**
  `magus\harness.provider(claudeMod)` keeps `extraKnownMarketplaces.magus` and
  `enabledPlugins["magus@magus"]` in `.claude/settings.json`, through a new
  `harness_settings` op any harness spell can use for plain host settings; a value
  someone set differently is named, never overwritten.
