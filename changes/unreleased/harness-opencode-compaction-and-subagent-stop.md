### Added

- **OpenCode briefs a compacted session and judges a subagent's end.** `session.compacted`
  puts the brief on the root session's next tool result, after the summary. A foreground
  task's `tool.execute.after` is its child's stop; a deny rides the parent's result with the
  `task_id` that resumes the child. A backgrounded child's idle is its stop, and its deny is
  prompted back.
