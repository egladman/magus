### Added

- **`magus status` names who owns each budget seat, who waits for one, and who was
  refused.** Each holder in `broker.capacity` carries an `owner`, `waiting` lists the
  requests parked for a seat, and `refused` keeps the last 20 MGS3009 refusals with their
  holders. The Claude Code mod reads them from the JSON.
