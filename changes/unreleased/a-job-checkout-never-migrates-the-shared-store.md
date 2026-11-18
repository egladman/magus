### Changed

- **Breaking: a magus stale against its checkout never writes the shared job store.** A
  binary built from another contract is refused with MGS1021 before any row is written,
  naming the relink. `hack/dev/bootstrap-checkout.buzz` opens the store with the placed
  `./magus` first, so a refusal stops the bootstrap.
  [State](https://eli.gladman.cc/magus/concepts/state/#store-migrations).
