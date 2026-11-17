### Changed

- **`magus run release` pins and proves every in-repo require.** It refuses a required
  tag nobody cut, naming the run that adds it, refuses pseudo-versions and dependencies
  that moved since their tag, and tags a commit whose `go.mod`s carry no in-repo replaces
  after proving `go get` and `go install` from outside the repo. The next commit restores
  the replaces.
