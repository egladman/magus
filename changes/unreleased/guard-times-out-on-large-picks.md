### Fixed

- **A large cherry-pick no longer runs the guard out of time.** Grading a pick opened the
  workspace again for each file another job held, which spent the hook's seven seconds
  before the workspace rules ran. A judgment now opens each root once. The denial for a
  judgment that runs out of time no longer says to retry; it says a retry passes only if
  judging costs less, and to report a call that lapses twice.
- **A refused merge names the branches the job integrates.** Those branches already merged
  by name. An integrator handed a commit id now learns the name to use.
