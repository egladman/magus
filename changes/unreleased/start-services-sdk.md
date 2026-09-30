### Changed

- **Breaking for SDK callers: `ServiceHost.Acquire` and `Client.AcquireService` also
  return whether the broker owns the service.** A service with neither `command` nor
  `start`, or with both, fails to load.
