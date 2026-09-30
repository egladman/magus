### Fixed

- **A long-running magus no longer grows with every magusfile evaluation.** Each Buzz
  session kept about 0.5 MB alive forever, so the daemon and benchmarks grew without
  bound. A session now releases its heap values when it closes; under `-race`, a value
  read after its session closed panics instead of reading another object.
