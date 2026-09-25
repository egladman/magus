### Security

- **Files that run code after the run are write-protected.** Under any mode but `off`,
  magus's checks refuse writes to `.git/hooks`, `.git/config`, `.git/info`, `magus.yaml`,
  mise and asdf pins, `.envrc`, `.claude/`, `.cursor/`, `.mcp.json`,
  `.vscode/tasks.json` and git hook managers' config. Landlock cannot deny inside a
  grant, so a child can still write them.
