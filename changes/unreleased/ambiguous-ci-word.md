### Added

- **The bare word "ci" no longer lets an agent assume which one.** The
  `ambiguous-ci-word` guard rule marks a session whose person said "ci" without
  naming the magus `ci` target or the host's CI/CD, and refuses the next call that
  picks either until they clarify. The magus-run skill names the same fork.
