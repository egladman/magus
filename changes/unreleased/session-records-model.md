### Added

- **Loaded sessions carry the model and host version.** `AgentEvent` and `Summary` gain
  one `agent` value (`model`, `host_version`) on the wire and in `-o json`. Adapters that
  cannot read them declare `none` rather than guess, and the coverage line moves to
  schema=2.
