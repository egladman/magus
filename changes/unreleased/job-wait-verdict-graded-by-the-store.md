### Changed

- **The job store grades `magus job wait`'s verdict itself.** A leased holder may only pass
  a row below its own lease and change nothing else on it; the store refuses any other
  verdict, not only the command.
