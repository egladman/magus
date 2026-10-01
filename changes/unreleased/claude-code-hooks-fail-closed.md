### Fixed

- **Claude Code guard hooks refuse the call when no magus resolves.** A bare `magus` with
  none on PATH exited 127, which Claude Code runs past, so every guard rule failed open.
  Each printed hook now runs the session root's `./magus`, else PATH's; with neither, a
  `PreToolUse` entry exits 2 naming `GOEXPERIMENT=jsonv2 go run -trimpath ./cmd/magus
  run go-build --no-cache .`.
