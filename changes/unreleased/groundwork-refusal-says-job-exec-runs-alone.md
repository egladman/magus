### Changed

- **`orchestrator-groundwork` says `job exec` runs alone on its line.** When the refused
  line also calls `magus job exec`, the refusal adds that a line is judged before it runs,
  so the row is taken on its own line and the rest follows.
