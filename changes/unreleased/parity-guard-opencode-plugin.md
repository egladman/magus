### Changed

- **The OpenCode plugin judges MCP, `task` and `apply_patch` calls.** Advice on an MCP
  call reaches the model, a spawn records its child session, the root session's reply
  is proofread at idle, and child sessions no longer record checkpoints. Copy the
  plugin again from the OpenCode guide.
