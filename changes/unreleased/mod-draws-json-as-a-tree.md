### Added

- **The Claude Code mod draws a tool result that is JSON or JSON Lines as a tree.**
  Every node starts closed and opens on a press; keys, brackets and value types
  keep JSON's colors, and Raw shows the text. A record's `api_version`, `kind` and
  name head its row while it is closed. The text around one JSON value, such as
  `Exit code 1` before it or a command's summary after it, still draws.
