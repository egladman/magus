### Changed

- **`magus agent harness apply|install|remove` is refused under a lease from any source.**
  The commands refuse under the checkout's binding as well as a `BAGGAGE` claim, and the
  guard refuses them for a worker attributed by its spawn or its session.
