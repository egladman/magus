### Changed

- **Breaking: built-in guard rules that name a remedy deny by default.** Rules with no
  reachable remedy became notices, which never refuse. Set any other rule to `advise`
  or `off` in the root magusfile. ADR 0013.
