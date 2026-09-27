### Changed

- **`magus agent harness install` is refused under a lease from any source.**
  It refuses under the checkout's binding as well as a `BAGGAGE` claim, and the guard
  refuses it for a worker attributed by its spawn or its session.
