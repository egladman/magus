### Changed

- **Breaking: BZZ1005 rejects `==` and `!=` between an enum and a `str`, `int`, `double`
  or `bool`.** A case never equals its backing value, so `decision == "deny"` was always
  false. Compare to a case (`Decision.deny`), convert first (`Decision("deny")`), or
  compare `.value`.
