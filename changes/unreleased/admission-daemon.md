### Changed

- **A daemon a run starts serves only the machine build budget.** It opens no HTTP
  listener, MCP, console, maintenance or VCS hooks, runs no work, and the run prints
  one line saying so. `magus status` lists each daemon with why it started and its
  listeners; `magus server start` replaces an idle one and refuses a busy one.
