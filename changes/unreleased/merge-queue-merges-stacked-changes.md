### Added

- **The merge queue merges stacked changes.** A change carrying another queued or merged
  change's head is stacked on it: it merges after it, its own delta measured from that
  head, and waits without blame when the one beneath is kicked back. On GitHub a
  `queue: <method>` label on a stack's top queues the stack.
