### Fixed

- **A large cherry-pick no longer runs the guard out of time.** Grading a pick opened the
  workspace again for each held file and spent the hook's seven seconds. A judgment now
  opens each root once. A timeout denial says a retry passes only if judging costs less.
