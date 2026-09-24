### Fixed

- **A run killed outright releases its claims and services at once.** Claims and
  service references ride the run's one broker connection, so the kernel closing it
  releases them; this replaces pid polling and the 24-hour cap. When the broker
  restarts under running steps, they re-assert their claims on the new one.
