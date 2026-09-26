### Changed

- **Breaking: the merge queue refuses a change that commits `.magus`.** Each candidate's
  hooks keep their caches in its checkout's `.magus`, beside magus's own, so a committed
  one would be replayed as the candidate's. A hook's `MAGUS_CACHE_DIR` and
  `XDG_STATE_HOME` point there too, whatever the queue was started with.
