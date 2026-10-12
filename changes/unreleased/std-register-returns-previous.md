### Changed

- **`std.RegisterJobGrader` and `std.RegisterVCSLeaseGate` return the registration they
  replaced**, as `agent.RegisterHarnessSpellLoader` does, so a test can put it back.
  `RegisterJobGrader` is also safe to call while a `magus\job` member runs.
