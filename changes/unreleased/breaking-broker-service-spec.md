### Changed

- **Breaking for SDK callers: a shared service crosses the broker wire as a
  `broker.ServiceSpec`.** `Client.AcquireService` and `ServiceHost.Acquire` take one
  instead of `spells.Service`, so a spell schema change never changes the protocol;
  `broker.NewServiceSpec` resolves a spell's service to one. A service acquire with no
  command is refused as `malformed`, and `Client.Shutdown` returns once the broker has
  hung up.
