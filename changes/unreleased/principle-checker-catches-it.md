### Added

- **A design principle: the checker catches it, the runtime is the backstop.** A mistake the
  Buzz checker, the spell contract, a generated decoder or a Go analyzer can find is refused at
  check time; a runtime refusal is for input the checker cannot see and names the typed fix.
