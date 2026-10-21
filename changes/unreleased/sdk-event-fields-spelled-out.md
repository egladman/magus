### Changed

- **`magus.Event` spells out its fields and `WithGraphHistory` names its argument.**
  `Inv` is now `Invocation`, `Ts` is `Timestamp` and `DurationMs` is a `Duration`; the JSON
  keys `inv`, `ts` and `duration_ms` are unchanged. `WithGraphHistory` takes the new exported
  `DurationPredictor`.
