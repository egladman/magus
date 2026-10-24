### Fixed

- **A worker moves a ref or the index only in its own checkout.** lease-vcs
  refuses `git switch`, `checkout`, `branch <name>`, `add` and the like (or the hg, sl
  and jj equivalents) outside the checkout the job's fork made, serving the call as
  `git -C <checkout> ...`; reads pass anywhere. A refused merge names each
  depends_on branch it would accept.
