### Added

- **Every harness binds each hook point, or says why it cannot.** A hook a harness spell
  lists names the points it binds (`points` on `HookCommand`: shell, edit, stop,
  subagent-stop, spawn, session-start, compact, mcp, attach). A new contract function,
  `mgs_listUnsupportedHooks`, returns the rest, each with a reason that cites the host's docs
  or says `not verified: <what>`. `magus doctor` fails `harness-hook-bindings` on a point
  with neither, and on an unsupported row a hook binds. The matrix is in the agents guide.
- **Codex runs the stop guard and briefs a new session.** Its `Stop` hook now runs the guard,
  and `SessionStart` on startup, resume and clear runs the brief, as compaction already did.
- **OpenCode runs the hooks its spell lists.** The plugin answers a root session's idle as a
  stop, sending a deny back as a follow-up prompt. It checks a written `.buzz` file and
  records each read through `magus-observe.buzz`. A new root session's brief rides its first
  tool result.
- **Cursor records the path a read reached**, as the other hosts do.
