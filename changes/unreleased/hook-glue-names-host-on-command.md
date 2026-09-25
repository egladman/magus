### Changed

- **Breaking: hook glue names its host on the command (guard template 18).** Each glue
  command carries `--agent-name <host>`, rendered from the harness spell's name.
  `__MAGUS_AGENT_NAME`, the `claude-code` default and Codex detection from `turn_id` are
  gone; a hook with no name is refused (MGS3024). Re-run `magus agent harness apply`.
