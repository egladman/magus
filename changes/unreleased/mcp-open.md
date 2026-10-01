### Security

- **Loopback `/mcp` trusts its bearer token alone.** It no longer checks the `Host` or
  `Origin` header (no MGS9007 there), and it answers any origin's CORS preflight with
  `Access-Control-Allow-Origin: *`, allowing the `Authorization`, `Mcp-Session-Id` and
  `Mcp-Protocol-Version` headers and exposing the session id. A browser page with a token
  works like any other MCP client. One without a token, a DNS-rebinding page included,
  still gets `401`, and CORS never allows credentials. The console's routes keep their
  `Host` and `Origin` checks.

### Added

- **`mcp.http` (`MAGUS_MCP_HTTP`) takes `/mcp` off the HTTP listener.** With
  `mcp.http: false` the server mounts no `/mcp` route on loopback, while `magus mcp`
  (stdio) and `/mcp` on the server socket keep serving, and the listener keeps the console
  and the health routes. `mcp.http: true` beside `mcp.enabled: false` is a config error.
