### Changed

- **A guard deny shows the person one line.** The hook reply puts a deny's verdict line where
  the person reads it and the rest (next step, details ref) where only the agent reads it:
  `additionalContext` on Claude Code and Codex, `agent_message` on Cursor. A Stop block
  splits the same way on Claude Code; `magus shell` writes the verdict line alone.
