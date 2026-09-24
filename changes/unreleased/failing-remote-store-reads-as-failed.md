### Fixed

- **A failing remote store reads as failed, not missed.** Both shipped cache spells throw
  on a failed request; `false` means not stored (get) or already stored (put).
