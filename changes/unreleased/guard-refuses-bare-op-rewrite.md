### Changed

- **A bound worker's rw run of a spell op on its own is refused.** `worker-check-only`
  held a rewrite to the write paths by what its target declares, and an op declares
  nothing, so `magus run markdown::dprint:rw .` passed and its writes surfaced only after
  the fact, as MGS3001. The refusal now names the magusfile target that calls the op, or
  the run without the charm when none does. A read-only run of the op still passes, and
  so does an op whose spell declares no `rw` charm under `default_charms: [rw]`.
