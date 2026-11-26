### Changed

- **Breaking: a check naming exactly the written packages may no longer narrow their
  tests.** A `-run` or `-skip` that leaves out a test of a written package is refused with
  MGS3040, as any other check is. Widen the pattern to cover every test, or drop the flag.
- **A fork derives a test postcondition for each project a job writes.** It runs the
  project's ci test op over the written files' testScope. A row's own check drops it only
  when it runs that op over every written operand with no `-run`, `-skip` or narrower
  operand.
