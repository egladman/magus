### Fixed

- **A target that asks the magus binary for its answer keys on that binary.** A body that
  calls `magus\describe` or forks a nested magus (`magus\run`, `magus\cmd`, and the rest),
  directly, through a helper, or through an imported Buzz module, gets a `magus:` key line
  naming the running build. A rules page rendered from `magus\describe.rule()` no longer
  replays the old catalog after a rebuild; every other key is unchanged.
- **A target no longer replays another target's declared output.** A target that inherits
  a project's outputs no longer records or restores a glob a sibling declares with
  `ctx.writesFiles`, unless it composes that sibling with `ctx.needs`. A cached `lint` had
  been writing a stale `gen/**` back over a regenerated one, so the drift check of
  `generate` failed again on every run.
- **A cached drift gate no longer passes over stale committed outputs.** A target that
  declares outputs and branches on `rw`, run without `rw`, replays a hit only while every
  output still reads as the entry recorded it; otherwise the check runs. A tree that still
  matches keeps replaying, so the generator is still skipped.
