### Added

- **`magus status` names who owns each budget seat, who waits for one, and who was
  refused.** In `broker.capacity`, each holder carries an `owner` (its job lease,
  `spawned by <job>`, or `unleased`), `waiting` lists the runs and `status --wait`
  reservations blocked on a seat now, and `refused` keeps the last 20 MGS3009 refusals
  the broker answered, newest first, with the holders at that moment. The Claude Code
  mod reads them from the JSON instead of scanning its own session's Bash output.
