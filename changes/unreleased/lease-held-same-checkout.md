### Fixed

- **A lease in one checkout no longer blocks a run in another over an ignored output.**
  An output the VCS ignores is a file of each checkout, so the guard makes a run wait only
  for a holder in the same checkout, or for any holder of an output the change commits.
