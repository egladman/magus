### Security

- **Loopback `/mcp` trusts its bearer token alone.** It no longer checks the `Host` or
  `Origin` header (no MGS9007 there), and it answers any origin's CORS preflight with
  `Access-Control-Allow-Origin: *`, allowing the `Authorization`, `Mcp-Session-Id` and
  `Mcp-Protocol-Version` headers and exposing the session id. A browser page with a token
  works like any other MCP client. One without a token, a DNS-rebinding page included,
  still gets `401`, and CORS never allows credentials. The console's routes keep their
  `Host` and `Origin` checks.
- **A refused `/mcp` request is unreadable to a page.** Only an authenticated response
  carries `Access-Control-Allow-Origin`, so a `401`, `403` or `429` reaches a cross-origin
  page as the same network error as a closed port.
- **Failed authentication on `/mcp` is throttled.** Each caller, keyed by `Origin` or else
  its peer, may fail 20 times a second in bursts of 40, then gets `429` with
  `Retry-After` and the new code MGS9030 (too many failed authentications).
  A valid token is never throttled.
- **Token checks answer from memory.** The server keeps the operator file and the token
  store in memory, reloaded when either changes on disk and at least once a second, so a
  refused token costs no file read. A revoke made by another process takes effect within a
  second.

### Fixed

- **`mcp.insecure_bind` binds the address it names.** The listener used to bind
  `127.0.0.1` whatever `mcp.address` said, so `0.0.0.0:7391` with `insecure_bind: true`
  listened on loopback only. It now listens on exactly that address, exposing `/mcp` over
  plaintext HTTP with a bearer token as its only guard, and the startup log names the
  bound address and warns. The operator token is still refused from any non-loopback peer.
- **`magus mcp --help` and `magus status` stop naming an HTTP `/mcp` that is off.** With
  `mcp.http: false` they say so and name stdio and the server socket, and
  `--probe=mcp` fails with that note instead of reporting `serving`.

### Added

- **`mcp.http` (`MAGUS_MCP_HTTP`) takes `/mcp` off the HTTP listener.** With
  `mcp.http: false` the server mounts no `/mcp` route on loopback, while `magus mcp`
  (stdio) and `/mcp` on the server socket keep serving, and the listener keeps the console
  and the health routes. `mcp.http: true` beside `mcp.enabled: false` is a config error.
