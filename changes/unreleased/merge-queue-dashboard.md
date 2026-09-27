### Changed

- **Breaking: the GitHub merge queue's status labels read `queue: <state>`.** A
  person's intent stays `merge-queue: <method>`; the queue's answer is `queue: queued`,
  `queue: kicked back` or `queue: needs regeneration`, with descriptions saying what to
  do. Delete the old `merge-queue:` status labels by hand. The advisor mute labels are
  now `advice: silence` and `advice: silence <name>`.
