### Fixed

- **A large cherry-pick no longer runs the guard out of time.** Grading a pick reopened the
  workspace for each file another job held, spending the hook's seven seconds. A judgment now
  opens each root once. A timed-out judgment no longer says to retry; it says report a call
  that lapses twice.
- **A refused merge names the branches the job integrates.** An integrator handed a commit
  id now learns the name to use.
