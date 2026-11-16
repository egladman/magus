### Changed

- **The reply check's notice that `proofread` is missing reaches the agent alone.**
  `proofread-reply.buzz` sends it on Claude Code as `hookSpecificOutput.additionalContext`
  under the event that fired, not as a `systemMessage` the person sees. Codex's `Stop`
  output has no field only the agent reads, so the notice is not printed there.
