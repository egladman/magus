### Changed

- **A checkout's integrator rebases, cherry-picks and reverts there.** It may rebase
  onto its base, a depends_on branch or a branch it integrates. A rebase that runs a
  command, stashes or moves another ref is still refused, and the root still pushes.
  An integration row keeps its role after a continue, and may run
  `magus agent harness install` there.
- **A worker may revert a commit on its own branch**, graded by the paths the reverted
  commits wrote, as a cherry-pick is.
- **A lease refusal lists at most five write paths**, then names
  `magus describe job <id>`. The lease-checkout refusal says when the checkout has no
  `./magus`.
