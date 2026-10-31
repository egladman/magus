### Fixed

- **One background symbol refresh per machine.** Each stale read in each checkout could
  detach its own refresh, about 1.8 GiB resident apiece. A machine now runs one at a time;
  a read that finds one running says so and starts nothing. The detached process exits once
  it has refreshed.
