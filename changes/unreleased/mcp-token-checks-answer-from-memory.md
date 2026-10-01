### Security

- **Token checks answer from memory.** The server keeps the operator file and the token
  store in memory, reloaded when either changes on disk and at least once a second, so a
  refused token costs no file read. A revoke made by another process takes effect within a
  second.
