### Fixed

- **An interrupted run no longer reports MGS3001 for writes it did not make.** Ctrl+C
  cut the descendant write audit's re-walk short, and every file it never reached read
  as removed, so a target that wrote nothing was blamed for emptying a nested project.
  The audit now finishes its walk, and the step reports its cancellation.
