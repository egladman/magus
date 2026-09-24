### Added

- **A subagent's shell and edit calls are graded under its job.** `magus shell --agent`
  takes the host's subagent id, and the command and path glue forward it (`HOST_AGENT_PATH`,
  default `agent_id`). Guard templates are at version 17; re-install them.
