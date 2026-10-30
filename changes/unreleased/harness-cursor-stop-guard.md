### Added

- **Cursor runs the stop guard on `stop` and `subagentStop`.** A deny returns as
  `followup_message`, which Cursor sends as the next message, so the agent resumes; a
  subagent is asked only when it completed.
