### Changed

- **A worker may commit in its own checkout.** `lease-vcs` now lets a worker lease commit, with any backend, cherry-pick or `git apply`, on its own branch in its leased checkout: a cherry-pick and `git diff | git apply -3 && git commit -C` get one answer.
