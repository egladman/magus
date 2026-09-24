### Changed

- **Console links carry a one-time code, never a token.** `#code=` lives a minute and
  works once; the console trades it at `POST /api/v1/token/exchange` for a 12-hour console
  token. The guard denies agents the commands that mint or revoke tokens
  (`credential-verb`) and the token files (`token-state`). A non-loopback `mcp.address`
  needs `mcp.insecure_bind: true`, and the operator token is refused off loopback.
