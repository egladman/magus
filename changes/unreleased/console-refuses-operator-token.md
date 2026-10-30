### Changed

- **Breaking: the console refuses an operator token.** An `mgo_` token in a link is no
  longer traded for a console token. The browser drops it, forgets one it stored
  earlier, and names `magus config console token create`. The daemon answers an `mgo_`
  token with 403 on every console route, including the three `magus diff` and
  `magus notes` call. Those commands now mint a short-lived console token and revoke it.
