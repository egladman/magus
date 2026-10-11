### Changed

- **`nonspecific` takes a failure you can find as the reason.** A review comment naming
  failing tests, a failed CI job, a command that fails or code that does not compile is left
  alone; one that names nothing ("something went wrong, please try again") is still reported.
