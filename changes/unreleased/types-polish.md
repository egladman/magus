### Changed

- **Breaking: a guard rule request's `threadKnown` is `threadFrom`.** `magus\guard.command`, `magus\guard.write` and `magus\guard.spawn` requests carry `threadFrom`, `"reported"` or `"inferred"`, in place of a boolean whose false meant inferred.
