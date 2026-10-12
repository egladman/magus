### Changed

- **`testkit.CaptureStdout` and `CaptureStderr` fail a parallel test and stop waiting on a
  child process that outlives the function.** They stop two seconds after the function
  returns and drop what the child writes later, where they used to hang until the test
  timeout.
