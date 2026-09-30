### Added

- **A service can outlive the command that starts it.** A `Service` declares `start`
  instead of `command` for a VM or a system unit, with `readiness` and `stop` required.
  One already passing its readiness probe is adopted and never stopped; one magus starts
  is stopped at idle and recorded for the crash reaper. The podman spell gains a
  `machine` op.
