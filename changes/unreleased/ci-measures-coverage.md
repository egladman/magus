### Changed

- **CD no longer reruns the test suites to draw the coverage badges.** CI's main run
  measures them with the new `coverage-measure` target and uploads the profiles. The CD
  site job follows that run, downloads them by run id, and renders them with
  `coverage-render`. A failed CI run, or one with no profiles, fails the site job by
  name.
