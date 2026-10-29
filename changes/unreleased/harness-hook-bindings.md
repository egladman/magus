### Added

- **Every harness binds each hook point, or says why it cannot.** A hook a harness spell
  lists names the points it binds (`points` on `HookCommand`: shell, edit, stop,
  subagent-stop, spawn, session-start, compact, mcp, attach). A new contract function,
  `mgs_listUnsupportedHooks`, returns the rest, each with a reason that cites the `https://`
  URL of the host's docs page, or says `not verified:` with the URL checked and the
  `YYYY-MM-DD` it was read. Any other reason fails the harness's load; a vendored schema is
  not a citation. `magus doctor` fails `harness-hook-bindings` on a point with no row, and
  on an unsupported row a hook binds. The matrix is in the agents guide.
- **Cursor runs the stop guard on `stop` and `subagentStop`.** A deny returns as
  `followup_message`, which Cursor sends as the next message, so the agent resumes; a
  subagent is asked only when it completed.
- **OpenCode briefs a compacted session and judges a subagent's end.** `session.compacted`
  puts the brief on the root session's next tool result, after the summary. A foreground
  task's `tool.execute.after` is its child's stop, and a deny rides the parent's result with
  the `task_id` that resumes the child; a backgrounded child's idle is its stop, and a deny
  is prompted back to it.
- **`hack/dev/bootstrap-harnesses.buzz --id` refuses an id no loaded harness has**, listing
  the loaded ones.
- **Codex runs the stop guard and briefs a new session.** Its `Stop` hook now runs the guard,
  and `SessionStart` on startup, resume and clear runs the brief, as compaction already did.
- **OpenCode runs the hooks its spell lists.** The plugin answers a root session's idle as a
  stop, sending a deny back as a follow-up prompt. It checks a written `.buzz` file and
  records each read through `magus-observe.buzz`. A new root session's brief rides its first
  tool result.
- **Cursor records the path a read reached**, as the other hosts do.
