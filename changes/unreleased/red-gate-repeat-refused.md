### Changed

- **A failed gate is not re-run over the same tree.** The guard refuses `magus affected ci`
  when the branch's newest gate failed, nothing in the tree changed since, and a target
  that failed it has not passed on its own. The refusal names each failing target and its
  `magus run <target> <project>`. A change to the tree, or a pass of every failing target,
  admits the next gate.
