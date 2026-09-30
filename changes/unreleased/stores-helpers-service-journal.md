### Fixed

- **A service record survives the crash it exists for.** The broker's journal was
  rewritten in place, so a power loss could tear a record that the next broker then
  deleted without stopping the service.
