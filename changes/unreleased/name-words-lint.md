### Changed

- **Breaking for SDK callers: `types.Path.RelativeTo` is `Relativize`, and
  `hint.ServableTo` is `hint.FilterServable`.** The new `namewords` analyzer in
  `libs/conventions` reports a function or method name ending in To, For or Of, or
  using Src, Dst, Cfg, Msg, Idx, Tmp or Ptr as a word.
