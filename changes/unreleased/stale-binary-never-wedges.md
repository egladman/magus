### Changed

- **A guard policy that fails to load refuses changes, naming the error.** A typo in
  the policy, or a rule a stricter checker now rejects, no longer leaves calls on the
  built-in rules alone. Reads, edits to the policy's own sources, writes outside any
  workspace, VCS recovery and a rebuild of `./magus` still run.
- **A stale `./magus` lets the steps that clear it through.** Finishing a series of
  picks (`git cherry-pick <commit>...`, `--skip`) and writes outside any workspace, such
  as memory or a scratchpad, now pass while the tree does not load.
- **`magus self` reads no `magus.yaml`.** `self update` is the fix a stale binary
  prints, and it now runs where that binary cannot decode the configuration. A pinned
  `required_version` names its release: `magus self update --version vX.Y.Z`.
- **A hook call is judged by the `./magus` of the checkout it lands in**, not the
  session's binary, when the two differ.

### Added

- **The guard holds back a change to the load contract.** A `git cherry-pick` or
  `git merge` that changes the host declarations, the API locks, the generated bindings
  or the Buzz checker is refused straight into a checkout whose tree loads, and a
  checkout whose `./magus` was built before HEAD changed them refuses state-changing
  commands until the rebuild. The merge script probes the guard policy with the merged
  binary before it lands such a change.
