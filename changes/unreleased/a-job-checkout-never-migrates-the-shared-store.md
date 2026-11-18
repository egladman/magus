### Changed

- **Breaking: a magus stale against its checkout never writes the shared job store.** A
  binary built from another contract than the checkout carries is refused with MGS1021
  before any row is written, naming the relink. `magus job`, `magus run`, the server and
  the guard all judge it. `hack/dev/bootstrap-checkout.buzz` opens the job store with
  the placed `./magus` before any step writes it, so a refusal stops the bootstrap.
  [State](https://eli.gladman.cc/magus/concepts/state/#store-migrations).
