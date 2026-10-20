### Changed

- **Breaking for SDK callers: gopherbuzz's `PoolObserver` takes a kind, not a bool.** `SessionAcquire` takes a `SessionAcquireKind` (`SessionReused`, `SessionCold`) and `SessionRelease` a `SessionReleaseKind` (`SessionRetained`, `SessionEvicted`).
