### Fixed

- **The workspace stop rule reads a running try's age correctly.** It no
  longer mixes the store's seconds with millisecond time, says so when no
  start is recorded, passes a stop the host marks as held already with its
  note kept as advice, and tells the root the host wakes it when a tracked
  background subagent ends.
