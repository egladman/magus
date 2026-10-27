### Changed

- **A job checkout forks at any checkpoint and builds its own `./magus`.** In a workspace
  holding `hack/dev/bootstrap-checkout.buzz`, `magus job fork --checkout` runs it in the new
  checkout instead of copying the forker's binary, so a forker built at another commit or
  from uncommitted changes no longer blocks the fork. A failed build removes the checkout
  and writes no row. Any other workspace gets a copy of the forker's binary.
