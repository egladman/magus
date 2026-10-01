### Changed

- **Graph builds no longer race on the knowledge store.** Every graph build, a manual
  `magus graph build` or the server's sync-graph job, takes one lock on the workspace's
  knowledge store. The second names the build it waits on, with its pid and start time,
  and then reads the graph that build stored. A project it left without a current index,
  or a build killed mid-way, still gets a build; a lock a killed build left behind is taken
  over with a notice.
- **A missing or stale symbol index says why.** `magus refs` and the guard's stale-graph
  advice name the cause they can observe, with its remedy: no server running, so the VCS
  refresh hook's sync did nothing; a refresh hook whose binary (such as `./magus`) does not
  exist in the checkout; no refresh hook installed; a server on another build; a sync
  running now. `magus job run sync-graph` records what each request did, so a hook that
  ran with no server is told apart from one that never ran.
