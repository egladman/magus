### Changed

- **An ask stops at the host's own prompt, or is refused.** A person-only command never runs
  because the host skipped a prompt. Claude Code and Codex may ask only under
  `permission_mode` `default`, `acceptEdits` or `auto`; Cursor only on `beforeShellExecution`
  and `beforeMCPExecution`. Any other ask is refused, with a message giving the command
  to run at the person's terminal.
