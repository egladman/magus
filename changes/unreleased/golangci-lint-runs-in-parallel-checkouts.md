### Fixed

- **golangci-lint runs in sibling checkouts at once.** golangci-lint held one lock in the
  temp dir, so a lint in one checkout failed another checkout's lint with `parallel
  golangci-lint is running`. The lint config now allows parallel runners; each checkout
  keeps its own lint cache.
