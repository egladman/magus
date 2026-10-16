### Changed

- **`magus job exit` with a result narrows the job to what it changed.** Write paths that
  cover no path in the result are recorded as releases, so an exited job awaiting
  `magus job wait` no longer holds files it never touched. An exit with no result keeps
  every path.
