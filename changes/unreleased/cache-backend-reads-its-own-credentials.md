### Fixed

- **A remote cache backend reads its credentials under its own sandbox declaration.** It
  ran under the served target's policy, so the GitHub Actions cache saw its URL and token
  as unset: every lookup missed and every store failed. The GitHub Actions spell now
  declares both, and no target, script or hook sees the token.
