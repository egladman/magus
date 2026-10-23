### Changed

- **The bootstrap names the outputs its build regenerated.** When a clean checkout builds a
  `-dirty` binary because its generators rewrote stale tracked outputs, the refusal lists each
  path and the `git add` and `git commit` that records them on the job's branch.
