### Fixed

- **Job checks read test selection from spell declarations.** Non-Go targets named
  `test` no longer receive Go narrowing rules. The Go spell declares its selector
  dialect, which renamed forwarding wrappers and qualified ops inherit. Complex
  wrappers report selection as unchecked and require matching recorded arguments;
  mentioning a Go op alone does not prove forwarding.
