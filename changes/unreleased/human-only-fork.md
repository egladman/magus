### Changed

- **`magus job fork` refuses a check its worker could never run because a person runs it.**
  A `--check`, or a goal's check, naming a `human_only` target is refused with the
  declared reason, and the refusal asks for a check a worker may run instead of a target
  that only compiles. Goal checks are graded against the guard as the row's own check is.
- **The agent guard hook reads the `human_only` targets from the knowledge store.** A
  `magus run` or `magus affected` line no longer opens the workspace to learn them while
  the store's record matches the tree; any record it cannot prove current is ignored and
  the workspace is opened as before.
