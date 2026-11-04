### Added

- **The guard holds back a change to the load contract.** A cherry-pick or merge that
  changes the host declarations, the API locks, the generated bindings or the Buzz checker
  is refused into a checkout whose tree loads. A checkout whose `./magus` predates such a
  change refuses state-changing commands until the rebuild.
