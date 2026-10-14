### Removed

- **Breaking: the guard no longer rewrites a shell command to close its stdin.** Claude Code
  and Cursor already hand a shell command `/dev/null` for stdin, and Codex never needed the
  prefix, so the `exec </dev/null;` rewrite is gone on every host, along with the
  `stdin-closed` rule, `magus shell --rewrites-input` and the verdict's `updated_command`.
  A workspace that names `stdin-closed` in `magus\guard.builtins` now fails to load with an
  error saying the rule was removed; delete the key. A hook wired with an older
  `magus-command.buzz` passes `--rewrites-input`, which `magus shell` now rejects; reinstall
  the hook glue.
