### Changed

- **Breaking (Go API): the cache's tiers share one shape.** `cache.WithMutable` is
  `WithLocalWrite`, `WithRemoteStats` is `ContextWithRemoteStats`, `Cache.Remote()` is
  `RemoteNamespace(ns)`, and `RemoteBackend` takes `(namespace, key)`, answers
  `ErrRemoteMiss` and `ErrRemoteExists` instead of `(nil, nil)`, and gains `HasArtifact`.
  `cache.Open` reads no environment.
