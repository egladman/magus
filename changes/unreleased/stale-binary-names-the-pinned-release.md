### Changed

- **MGS1021 and the guard name the release a pinned `required_version` admits.** An exact
  pin prints `magus self update --version vX.Y.Z`, and a range with an upper bound prints
  its lower bound, since the newest release may sit past the bound.
