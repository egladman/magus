### Changed

- **`magus run release` pins and proves every in-repo require.** It cuts tags requires
  name but nobody cut, refuses pseudo-versions and dependencies that moved since their
  tag, and tags a commit whose `go.mod`s carry no in-repo replaces after proving `go get`
  and `go install` from outside the repo. The next commit restores the replaces.
