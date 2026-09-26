### Changed

- **Breaking: the merge queue refuses a change that commits `.magus`.** Each candidate's
  hooks keep their caches in its checkout's `.magus`, beside magus's own, so a committed
  one would be replayed as the candidate's. Removing a candidate also removes the
  read-only module cache Go leaves there.
