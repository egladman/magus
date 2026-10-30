### Changed

- **An ask stops at the host's own prompt, or is refused.** A person-only command never runs
  because the host skipped a prompt. On Claude Code and Codex it may ask only under
  `permission_mode` `default`, `acceptEdits` or `auto`; any other mode, or none, refuses it.
  Cursor renders an ask only on `beforeShellExecution` and
  `beforeMCPExecution`, and refuses any other ask on `preToolUse`, which it does not enforce.
  A refusal tells the person what happened, why this host cannot ask, and the command to run
  at their terminal, through `systemMessage` or `user_message`.
