### Security

- **Buzz `http.server` refuses a request whose `Host` or `Origin` is not loopback.** It
  bound 127.0.0.1 and served anyone who reached the port, so a page that rebound its own
  name to 127.0.0.1 could read the served tree.
- **Cache, output, session, trail and job state is private to its owner.** Directories
  are created `0700` and files `0600`, as the token files already were. They were `0755`
  and `0644`, so another local user could read build logs and agent payloads. A directory
  made by an older magus keeps its mode.
- **The `/mcp` auth-failure throttle keys on the peer address alone.** It keyed on the
  `Origin` header, which any client but a browser can set, so each invented value got a
  fresh allowance.
