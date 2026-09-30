### Fixed

- **A trail rotation keeps `events.jsonl` readable at the mode it was created with.** It
  also flushes the file, so a crash mid-rotation cannot empty the trail.
