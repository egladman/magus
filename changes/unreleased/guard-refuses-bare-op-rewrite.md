### Changed

- **A bound worker's rw run of a spell op on its own is refused.** An op declares no
  writes, so `magus run markdown::dprint:rw .` passed `worker-check-only` and its writes
  surfaced only after the fact, as MGS3001. The refusal names the magusfile target that
  calls the op, or the run without the charm when none does.
