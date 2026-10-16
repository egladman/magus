### Fixed

- **A step's process group the broker stops is filed in the run's attention queue.**
  The broker recorded the stop only in its own log and in `magus status`, because the
  run never told it which workspace the group ran in.
- **The broker signals a held process group only while it is still that group.** A hold
  left behind after its group emptied could make the broker stop an unrelated group
  that reused the id. The broker now checks that the group's leader runs and started
  before the hold; when the leader has exited it cannot tell, so it leaves the group
  running and files why in the attention queue.
