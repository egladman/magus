### Changed

- **BREAKING: a token holds a grant, and every route names what it needs.** A grant is
  `none`, `read` or `write` per surface (`tokens`, `mcp`, `console`); a valid token below
  a route's need gets 403 MGS9015. No door mints a token wider than its minter's grant.
  Doctor's `mcp-tokens` check is now `tokens`.
