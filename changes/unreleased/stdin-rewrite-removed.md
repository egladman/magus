### Removed

- **Breaking: the guard no longer rewrites a shell command to close its stdin.** Claude Code and Cursor already hand a shell command `/dev/null` for stdin, and Codex never needed the prefix, so the `exec </dev/null;` rewrite is gone on every host, along with the `stdin-closed` rule, `magus shell --rewrites-input` and the verdict's `updated_command`.
