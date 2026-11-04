### Fixed

- **A start service that fails to start but is ready is adopted.** `podman machine
  start` exits 125 when the machine is already running, which failed
  `service.acquire`. The runner now probes readiness once after a failed start,
  adopts the service when it passes, and prints a line naming the start's exit status.
