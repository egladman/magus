### Changed

- **`magus graph build` waits for the server's sync-graph instead of racing it.** When the
  server is already rebuilding this workspace's graph, the build names that job, its id and
  the server's pid, waits for it, and reads the graph it stored. A project the job left
  without a current index, or a server that stops mid-job, still gets a build.
- **A missing or stale symbol index says why.** `magus refs` and the guard's stale-graph
  advice name the cause they can observe, with its remedy: no server running, so the VCS
  refresh hook's sync did nothing; a refresh hook whose binary (such as `./magus`) does not
  exist in the checkout; no refresh hook installed; a server on another build; a sync
  running now. `magus job run sync-graph` records what each request did, so a hook that
  ran with no server is told apart from one that never ran.
