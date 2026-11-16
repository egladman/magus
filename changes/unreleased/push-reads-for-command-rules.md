### Added

- **A command rule reads what a push publishes and which of its files no run read.**
  `req.push` names the commit, the checkout and whether the line opens a pull request.
  `magus\workspace(dir).unverified(revision)` lists the changed files no clean passing run read.
  Until the built-in push rule is removed, both it and this repository's `push-unverified` rule speak on a push.
