### Fixed

- **A cached drift gate no longer passes over stale committed outputs.** A target that
  branches on `rw`, run without it, replays a hit only while every declared output still
  matches the entry, mode included; otherwise the check runs.
