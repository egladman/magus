### Added

- **Loaded sessions carry the model and host version.** `AgentEvent` gains `model` and
  `host_version`; the Claude Code adapter emits them from `.message.model` and `.version`,
  and Codex and OpenCode declare `none` rather than guess. The coverage line moves to
  schema=2. `session show` and `session ls -o json` report the pair off the newest event
  that named one.
