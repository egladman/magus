### Fixed

- **A step's process group the broker stops is filed in the run's attention queue.**
  The broker recorded the stop only in its own log and in `magus status`, because the
  run never told it which workspace the group ran in.
- **A step's process group survives a broker restart and still ends with its run.** A
  stopping broker stopped every group a run had handed it, and a run re-asserted only
  its claims on the next broker, so a group started before a broker crash outlived a
  run that died after it. A stopping broker now leaves the groups to their runs, and
  each run hands them to the next broker with its claims.
- **The broker signals a held process group only while it is still that group.** A hold
  left behind after its group emptied could make the broker stop an unrelated group
  that reused the id. The broker now checks that the group's leader runs and started
  before the hold; when the leader has exited it cannot tell, so it leaves the group
  running and files why in the attention queue.
- **A failing sibling step no longer cancels the hold on a step's process group.** A
  hold cancelled mid-request could leave one the broker recorded and the run never
  released. A release the broker does not answer now warns that it may still hold the
  group.
