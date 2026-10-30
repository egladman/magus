### Added

- **Every harness binds each hook point, or says why it cannot.** Each hook a harness spell
  lists names the points it binds (`points` on `HookCommand`). A new contract function,
  `mgs_listUnsupportedHooks`, returns the rest, each citing the host's docs URL or saying
  `not verified:` with a date. `magus doctor` fails `harness-hook-bindings` on a point with
  no row.
- **Cursor runs the stop guard on `stop` and `subagentStop`.** A deny returns as
  `followup_message`, which Cursor sends as the next message, so the agent resumes; a
  subagent is asked only when it completed.
- **OpenCode briefs a compacted session and judges a subagent's end.** `session.compacted`
  puts the brief on the root session's next tool result, after the summary. A foreground
  task's `tool.execute.after` is its child's stop; a deny rides the parent's result with the
  `task_id` that resumes the child. A backgrounded child's idle is its stop, and its deny is
  prompted back.
- **`hack/dev/bootstrap-harnesses.buzz --id` refuses an id no loaded harness has**, listing
  the loaded ones.
- **Codex runs the stop guard and briefs a new session.** Its `Stop` hook now runs the guard,
  and `SessionStart` on startup, resume and clear runs the brief, as compaction already did.
- **OpenCode runs the hooks its spell lists.** The plugin answers a root session's idle as a
  stop, sending a deny back as a follow-up prompt. It checks a written `.buzz` file and
  records each read through `magus-observe.buzz`. A new root session's brief rides its first
  tool result.
- **Cursor records the path a read reached**, as the other hosts do.
- **`magus shell` refuses an `--agent-name` no installed harness names.** Where any
  harness's tool rows are installed, a call whose hook entry names another host is denied,
  listing the installed ones, instead of judged without its host's facts. The deny's next
  step is `magus agent harness install --id <name>`.
- **A harness declares the payload fields only its host sends** (`payload_fields` in
  `mgs_getLimits`, each cited from the host's docs). A hook payload carrying another
  installed host's field and none of the named host's own is denied, naming both; the next
  step is `magus describe harness`, whose entries each name their own host.
