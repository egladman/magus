### Changed

- **Breaking for SDK callers: the broker wire carries a service spec and error codes.**
  `Client.AcquireService` and `ServiceHost.Acquire` take a `broker.ServiceSpec` instead of
  `spells.Service`. Every refusal matches by code with `errors.Is` against
  `broker.ErrNoServices`, `ErrProtocol` and their siblings, and `Shutdown` returns once
  the broker hangs up.
