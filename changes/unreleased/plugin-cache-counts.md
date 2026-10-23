### Fixed

- **Doctor finds a plugin Claude Code cached.** The claude plugin check counts the
  magus mod as installed when `installed_plugins.json` lists it or when a version
  directory sits under `cache/<marketplace>/<plugin>/`, which is where Claude Code puts
  a relative-path marketplace's plugin without recording it. Both are read under
  `CLAUDE_CODE_PLUGIN_CACHE_DIR` when set. The missing-plugin hint now names
  `claude plugin marketplace add egladman/magus`.
