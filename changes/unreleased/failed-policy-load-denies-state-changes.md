### Fixed

- **A guard policy that does not load denies every state change.** Before, only a gated verb was
  denied and the rest ran on the built-in rules with a note. Now a push, an edit or any other
  change waits until a side of the policy loads. Reads, edits to the policy's own sources, VCS
  recovery and a rebuild of `./magus` still run. The note that a workspace rule judged nothing is
  told in full on every call, and the light-level built-ins that stood in for an unresolved
  approved side are gone: the resolve error is reported as the load failure.
