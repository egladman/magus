### Added

- **`magus\service\acquire(spell, op:)` holds a shared service from a `magus buzz`
  script.** It returns a `ServiceLease` saying whether magus owns the service, routes
  through the broker as a target's does, and every lease is released when the script
  ends. `hack/remote/on-linux.buzz` holds the podman machine this way instead of refusing a
  stopped one.
