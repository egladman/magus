### Fixed

- **`magus agent harness verify` and `magus doctor` fail when the Claude Code mod is not
  installed.** A `.claude/settings.json` that enables the plugin no longer passes while
  Claude Code has never installed it; the error names the per-person install command.
