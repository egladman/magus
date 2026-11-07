### Changed

- **Breaking: `knowledge.workspaces` entries are `{name, path}` and live only in the user
  config.** A bare root or the key in a repository's magus.yaml refuses, and
  `MAGUS_KNOWLEDGE_WORKSPACES` is gone. `--global` qualifies node ids by entry name, not
  directory basename, and refuses an unlisted current workspace or an entry that fails to
  open, which it used to skip.
- **A read pattern reaching outside the workspace is refused.** `ctx.readsFiles` with a
  `..` past the root or an absolute path used to key nothing, silently. It now fails the
  run and names `knowledge.workspaces`.
