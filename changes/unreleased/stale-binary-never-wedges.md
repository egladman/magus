### Changed

- **A guard policy that fails to load refuses changes, naming the error.** A typo in the
  policy, or a rule a stricter checker now rejects, no longer leaves calls on the built-in
  rules alone. Reads, edits to the policy's own sources, writes outside any workspace, VCS
  recovery and a rebuild of `./magus` still run.
