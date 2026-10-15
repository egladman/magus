### Changed

- **Breaking: a guard rule request's `context` is `window`.** It is a `ContextWindow` (formerly `AgentContext`), in place of `context`, which read as the handed prompt.
