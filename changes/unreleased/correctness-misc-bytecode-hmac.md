### Fixed

- **Security: cached guard-rule bytecode is authenticated.** Every chunk under
  `<user cache>/magus/buzz-bytecode` carries an HMAC keyed by a secret in
  `<user state>/magus/buzz-bytecode.key`. A chunk that does not verify, planted or moved
  from another key, is compiled over rather than run.
