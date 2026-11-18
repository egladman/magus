### Fixed

- **The job store fence judges a build by the stamp `magus version` prints.** A clean
  go-build binary on the base migrates the shared store again, whatever context opened
  it. An unstamped binary falls back to Go's VCS stamp and says so; one with neither is
  refused naming `./magus run go-build .`.
