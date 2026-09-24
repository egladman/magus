### Changed

- **Breaking: `magus status` reports the broker and the server one fact per row.**
  `-o json` gains `broker`, `server` and `broker_policy`, moves `machine` and
  `services` under `broker`, and drops `pool.mode`; `pool.daemon_version` is
  `pool.version`. `magus version -o json` reports `server` for `daemon`. `magus
  broker status` exits non-zero when none is running.
