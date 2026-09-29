### Fixed

- **Overlapping runs no longer erase each other's test history.** The volatility and
  duration history is shared by every workspace on the machine; a run wrote back the
  copy it loaded before it started, dropping outcomes another run recorded meanwhile.
  `magus config history passed` and `import` had the same race.
- **Concurrent graph builds no longer leave the knowledge graph stale.** Two queries
  in one checkout could leave the manifest naming a shard the file on disk did not
  hold, and later builds never rewrote it.
- **Memory entries keep frontmatter an older magus does not know**, and two updates
  of one entry no longer drop each other's fields.
- **A service record survives the crash it exists for.** The broker's journal was
  rewritten in place, so a power loss could tear a record that the next broker then
  deleted without stopping the service.
- A remote output fetched by ref is filed by rename, so a concurrent reader never sees
  it half written, and a review's seen threads never come back as new after two
  processes save them.
- `magus session load` stops waiting for another loader when it is interrupted.
