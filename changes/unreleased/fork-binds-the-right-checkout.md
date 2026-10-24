### Changed

- **A forked job binds the checkout it runs in.** `job fork --checkout` adopts an existing
  linked checkout, a fork inside a job's checkout records it, and an integration job
  forked from the forker's linked checkout needs `--checkout`. `job apply` widening an
  exited or ended job re-declares it, and the sibling-checkout denial names the other
  checkout's own binary.
