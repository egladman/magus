### Changed

- **Breaking for SDK callers: `types.Path.RelativeTo` is `Relativize`, and
  `hint.ServableTo` is `hint.FilterServable`.** The new `namewords` analyzer in
  `libs/conventions` reports a function or method name ending in To, For or Of, or
  using Src, Dst, Cfg, Msg, Idx, Tmp or Ptr as a word.

### Removed

- **Breaking: proofread's `name-suffix` rule (PRF6003) is gone.** `namewords`
  checks every function and method name, not only those with a doc comment.
