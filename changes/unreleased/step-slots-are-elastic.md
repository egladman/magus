### Changed

- **A step runs with the slots the broker grants it.** A target that declares
  `slots` claims that width as floor and ceiling; any other asks from one slot up.
  Its limiter seat, jobserver, `GOMAXPROCS` and recorded slots follow the grant. A
  broker without grants seats full width, said once. A composer's retake asks the
  width it already runs with.
