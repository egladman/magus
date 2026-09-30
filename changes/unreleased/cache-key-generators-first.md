### Fixed

- **The first run after a change is cached.** A composer's skip_cache generators run once
  its dependencies finish and before its key, on a miss as well as a hit, once per
  invocation however many composers reach them. Their own members cache as steps, as
  under an uncached composer.
