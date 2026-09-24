### Added

- **`magus\guard.write` registers a workspace file-write rule.** One Buzz function sees
  every file an agent writes through its host's edit tools, with the text the host says
  it writes, on the same strengthen-only, fail-open, committed-copy terms as
  `magus\guard.command`. A command rule judging `git push` also gets the checkout's
  branch and its remotes' branches, read through the VCS driver.
