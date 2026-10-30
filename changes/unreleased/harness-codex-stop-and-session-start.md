### Added

- **Codex runs the stop guard and briefs a new session.** Its `Stop` hook now runs the guard,
  and `SessionStart` on startup, resume and clear runs the brief, as compaction already did.
