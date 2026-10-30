### Changed

- **An ask stops at the host's own prompt, or is refused.** A person-only command never runs
  because the host skipped a prompt. Claude Code now refuses one under `bypassPermissions` and
  `dontAsk`, as Codex already did. Cursor renders an ask only on `beforeShellExecution` and
  `beforeMCPExecution`, and refuses any other ask on `preToolUse`, which it does not enforce.
  A refusal tells the person what happened, why this host cannot ask, and the command to run
  at their terminal, through `systemMessage` or `user_message`.
