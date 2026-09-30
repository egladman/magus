### Fixed

- **Console links carry a runnable command.** `magus job fork`, `ls jobs` and the other
  console hints print `open "<url>#token=$(magus config token print)"`, and
  the `console` MCP tool returns it as `open`. The job hint names a daemon running a
  different build instead of claiming nothing serves the console.
