### Changed

- **MGS1021 names the release a pinned `required_version` admits.** An exact pin
  prints `magus self update --version vX.Y.Z`, and a range with an upper bound prints
  its lower bound, since the newest release may sit past the bound.
- **A guard hook reads enum cases without compiling.** A rule's verdict and request
  take their enum cases from the definitions the session already holds, so a warm hook
  compiles nothing past the policy.
