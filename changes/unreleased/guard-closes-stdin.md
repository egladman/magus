### Changed

- **The guard runs an agent's shell commands with stdin at end-of-file.** On Claude Code the
  PreToolUse reply hands the command back as `updatedInput`, prefixed `exec </dev/null; `,
  with every other tool input field kept. An agent's command inherited a stdin nobody wrote
  to, so a stray reader (grep with no file operand, read, a prompt, ssh, a pager) waited
  forever and the host backgrounded it instead of killing it. A heredoc, a pipe or a `<`
  still feed their command. The guard never rewrites a denied or asked call, never prefixes
  twice, says so once per session (advisory `stdin-closed`), and records `stdin_closed` on
  the verdict in the activity trail. `magus shell --rewrites-input` declares a wiring that
  can carry the rewrite. Codex and Cursor are not rewritten: Codex applies `updatedInput`
  only beside `permissionDecision: "allow"`, which would turn a pass into an approval, and
  Cursor rewrites only at `preToolUse`, not at `beforeShellExecution` where magus gates a
  shell command.
