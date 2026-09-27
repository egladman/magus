### Changed

- **`vcs\history` takes `paths` and `first_parent`, and each commit carries `files`.**
  It reads any number of commits in one VCS call on git, hg, sl and jj, and `limit: 0`
  returns them all. A merge is judged by its diff against the first parent. The docs site
  reads its page history this way and no longer shells out to git.
