### Changed

- **Graph builds no longer race on the knowledge store.** Every graph build takes one lock
  on the knowledge store. The second names the build it waits on, with its pid and start
  time, then reads the graph that build stored. A project still lacking a current index
  gets a build; a killed build's lock is taken over with a notice.
