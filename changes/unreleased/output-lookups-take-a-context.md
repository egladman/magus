### Changed

- **Breaking for SDK callers: `OutputByRef`, `OutputAttempts`, `OutputDescriptorByRef` and
  `OutputKeyInputs` take a `context.Context` first.** A miss now reads other checkouts'
  stores; the context ends that search and carries the notice naming the checkout read.
