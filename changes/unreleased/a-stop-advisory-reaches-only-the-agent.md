### Changed

- **A guard advisory at `Stop` or `SubagentStop` reaches the agent alone.** The Claude
  Code command template sends it as `hookSpecificOutput.additionalContext` instead of a
  `systemMessage`, which the host shows the person. Codex's `Stop` reply has no field
  only the agent reads, so an advise there prints nothing. A deny is unchanged.
