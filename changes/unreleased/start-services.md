### Added

- **A service can outlive the command that starts it.** A `Service` declares `start`
  instead of `command` for a VM or a system unit, with `readiness` and `stop` required.
  One already passing its readiness probe is adopted and never stopped; one magus
  starts is stopped at idle, and only that one is recorded for the crash reaper. The
  podman spell gains a `machine` op built this way.
- **`magus\service\acquire(spell, op:)` holds a shared service from a `magus buzz`
  script.** It returns a `ServiceLease` saying whether magus owns the service, routes
  through the broker as a target's does, and every lease is released when the script
  ends. `hack/on-linux.buzz` holds the podman machine this way instead of refusing a
  stopped one.

### Changed

- **Breaking for SDK callers: `ServiceHost.Acquire` and `Client.AcquireService` also
  return whether the broker owns the service.** A service with neither `command` nor
  `start`, or with both, fails to load.

### Fixed

- **The broker's crash reaper records every hosted service.** A service keyed by its
  workspace path was never written to the journal, so a new broker had nothing to stop.
