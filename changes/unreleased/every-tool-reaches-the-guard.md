### Changed

- **Every tool call reaches the guard.** The Claude Code, Codex and Cursor harnesses wire one catch-all `PreToolUse` entry (`*`) to `magus-command.buzz`; the OpenCode plugin hands every tool to `magus shell`. The guard judges a call by its tool and input shape: a command, file read or write, search, spawn, skill load or MCP call.
