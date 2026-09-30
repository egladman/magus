### Removed

- **Per-verb MCP tools the magus module already covers.** `describe`, `describe_file`,
  `insight`, `run_target`, `doctor`, `memory`, `query`, `output`, `explain`, `refs`,
  `path`, `stats`, `vcs_checkpoint`, `job`, `where`, `run_affected`, `affected_plan` and
  `affected_explain` are gone. Call the matching `magus\` member from `client`, which
  does not offer `magus\cmd` or `magus\pry`.
