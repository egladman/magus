### Changed

- **A job checkout forks at any checkpoint and builds its own `./magus`.** In a workspace
  holding `hack/dev/bootstrap-checkout.buzz`, `magus job fork --checkout` runs it in the new
  checkout instead of copying the forker's binary, so a stale or dirty forker never blocks
  the fork. A failed build removes the checkout. Other workspaces get a copy.
