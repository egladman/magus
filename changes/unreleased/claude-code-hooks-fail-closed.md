### Fixed

- **Claude Code guard hooks refuse the call when no magus resolves.** A bare `magus`
  with none on PATH exited 127, which Claude Code runs past as a non-blocking error,
  so every guard rule failed open for the whole session. Each hook the claude-code
  harness prints now runs the session root's `./magus` (`$CLAUDE_PROJECT_DIR/magus`),
  else PATH's, and with neither a `PreToolUse` entry exits 2 naming
  `go run -trimpath ./cmd/magus run go-build --no-cache .`, the one command it lets
  through. Merge what `magus describe harness claude-code` prints.
- **`magus` means the checkout's build in a session and under magus.** A new
  `SessionStart` entry puts the session root first on PATH for the agent's Bash tool
  commands, and every child magus starts gets the running binary's directory first on
  PATH.
- **`magus doctor` fails when the hook interpreter is another build.** `guard-binary`
  runs `version` on the magus a hook would run and fails, naming both binaries, when
  it differs from the doctor's own or prints none.
