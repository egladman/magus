### Fixed

- **A leased script can commit in a repository of its own.** `vcs\cmd` under a lease may run `git commit` outside the job's checkout, in another repository such as a test's scratch repository. No other change there is allowed, and a commit in the job's own repository follows the usual rules. A write blocked by a declaration claim names the claim.
