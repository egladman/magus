### Fixed

- **The unchanged-inputs hint no longer reads as a verdict.** It prints before the
  target runs, so it now says the target runs again, and it names the failed attempt
  rather than the step ref, which moved to the new run's result once that run landed.
  The target always re-ran; the exit status was already that run's own.
