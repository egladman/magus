### Changed

- **BREAKING: `cache.remote.write.enabled: true` fails Open when no remote backend can
  write.** A wired backend that does not start, or no backend at all, is an error naming
  the cause. Unset or false, the run stays local-only and the cache header names why:
  `local (remote <name> unavailable: <err>)`.
