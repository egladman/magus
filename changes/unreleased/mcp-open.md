### Security

- **Loopback `/mcp` trusts its bearer token alone.** It no longer checks `Host` or
  `Origin` (no MGS9007 there) and answers any origin's CORS preflight, so a browser page
  with a token works like any other MCP client. A page without a token, a DNS-rebinding
  page included, still gets `401`, CORS never allows credentials, and the console's routes
  keep their checks.
