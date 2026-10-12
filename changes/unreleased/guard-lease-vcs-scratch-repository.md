### Changed

- **A leased Buzz script may run git in a scratch repository of its own.** `vcs\cmd`
  under a worker lease runs any git operation but a push in a repository wholly outside
  the job's checkout, such as a test's temporary repository. Inline config, a `GIT_*`
  location variable, or a work tree inside the checkout still refuses it.
