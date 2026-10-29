### Changed

- **A guard deny shows the person one line.** The hook reply puts a deny's verdict line where
  the person reads it and the rest (the next step, the details ref) where only the agent reads
  it: `additionalContext` on Claude Code and Codex, `agent_message` on Cursor. A Stop block
  splits the same way on Claude Code, and `magus shell` writes the verdict line alone to
  stderr.
- **Each harness spell declares its reply channels.** `mgs_getLimits` returns
  `channels`, from `agent`, `user` and `both`. A harness with hooks that declares none, or names
  another, fails to load. A host with no `agent` channel, OpenCode today, gets each deny as its
  verdict line alone.
