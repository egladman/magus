### Fixed

- **The push gate accepts a passing `ci` recorded on any commit with the pushed commit's
  tree.** A branch replayed onto main by cherry-pick or rebase could never be pushed: the
  gate keyed on the commit, while the gate-repeat refusal keyed on the tree. That refusal
  now names the run it points at.
