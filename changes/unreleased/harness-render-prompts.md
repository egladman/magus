### Changed

- **Every harness exports `mgs_renderPrompts`.** Codex and OpenCode still
  write the approval files their hooks cannot raise. Claude Code, Cursor
  and the Claude Code mod return an empty list, because the hook reply is
  the prompt. A harness that omits the function no longer loads.
