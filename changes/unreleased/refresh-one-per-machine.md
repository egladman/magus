### Fixed

- **One background symbol refresh per machine.** Each stale read in each checkout could
  detach its own refresh, about 1.8 GiB resident apiece on this repository. A machine now
  runs one at a time; a read that finds one running says so and starts nothing. The refresh
  claims the machine budget for its own work, and the detached process exits once it has
  refreshed instead of going on to answer a read nobody sees.
