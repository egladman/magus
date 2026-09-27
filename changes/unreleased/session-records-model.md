### Added

- **Loaded sessions carry the model and host version.** `AgentEvent` and `Summary` gain
  one `Agent` value (`model`, `host_version`), nested as `"agent": {...}` on the wire and
  in `-o json`; the Claude Code adapter emits it from `.message.model` and `.version`,
  and Codex and OpenCode declare `none` rather than guess. The coverage line moves to
  schema=2. `session show` and `session ls -o json` report the pair off the newest event
  that named one.
