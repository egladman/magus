### Fixed

- **The job store fence judges a build by the stamp `magus version` prints.** A clean
  go-build binary on the base migrates the shared store again, whatever context opened
  it. An unstamped binary falls back to Go's VCS stamp and says so; one with neither is
  refused naming `./magus run go-build .`.
- **The server adoption gate judges a build by its stamp too.** A clean stamped build in a
  linked worktree keeps its version as its identity, so a client adopts into a server
  built at the same commit. Go's VCS stamp is read only for an unstamped build.
