### Fixed

- **The broker signals a held process group only while it is still that group.** A
  stale hold could stop an unrelated group that reused the id. The broker now checks
  that the group's leader runs and started before the hold; when the leader has exited
  it leaves the group running and files why in the attention queue.
