### Changed

- **The trail names the credential, not "operator", and drops `actor`.** Each event records
  `credential` (the verified token's class, id, name and grant, never its secret) and the
  MCP client as `host`; the wire's `actor` is a label rendered from them. Console review
  comments are `unattributed`, not `human`.
