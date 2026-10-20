### Changed

- **Breaking for SDK callers: one `types.AgentFacts` holds a caller's cost facts.** The trail's `AgentCommand`, `AgentSpawn` and `SessionCall` and `observability.AgentCall` embed it, so the trail's JSON keys are unchanged; `AgentCall.Model` is `ContextModel`.
