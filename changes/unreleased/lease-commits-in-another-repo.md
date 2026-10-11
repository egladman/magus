### Fixed

- **A leased script can use git in a repository of its own.** `vcs\cmd` under a lease no longer refuses a call in a repository other than the job's, such as a test's scratch repository. The job's own repository stays refused. A write blocked by a declaration claim now says the file is outside your paths and names the claim.
