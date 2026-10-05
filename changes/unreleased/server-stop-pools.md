### Fixed

- **`magus server stop --pools` stops leftover pool parents from another build.**
  A live `magus mcp` or run that outlived a rebuild still hosts a per-process pool; status
  used to recommend `server stop`/`start`, which never touches those parents. Doctor's
  `sockets` check fails with a `--fix` remedy, and status names the pool parent and the
  same stop flag.
