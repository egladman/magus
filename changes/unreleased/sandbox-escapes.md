### Security

- **Buzz's own `os` and `io` go through the sandbox,** each run's `env\set` stays in
  that run, the proc socket demands a token (`MAGUS_PROC_TOKEN`), and the lease marker
  moved out of the cache dir a confined run can write.
