### Changed

- **The trail names the credential, not "operator", and drops `actor`.** Each event records
  `credential` (the verified token's name: `cli`, a connector or console token's name,
  `share`) and the MCP client as `host`; the wire's `actor` is a label rendered from them.
  Console review comments are `unattributed`, not `human`. Tokens may not be named `cli`
  or `share`.
