### Changed

- **Breaking: the console refuses an operator token.** An `mgo_` token in a link is no
  longer traded for a console token. The browser drops it and names
  `magus config console token create`. The daemon answers an `mgo_` token with 403 on every
  console route. `magus diff` and `magus notes` now mint a short-lived console token and
  revoke it.
