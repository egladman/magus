### Added

- **Every host delivers `advise` verdicts; three rehydrate after compaction.** Codex
  receives advisories and wires `SessionStart` on `compact`, Cursor's write guard moves to
  `preToolUse`, and OpenCode joins advisories by `callID`. Guard template v12: re-copy
  installed copies.
