### Fixed

- **The `encoding/json` build writes U+FFFD as v2 does.** A `RawMessage` or `MarshalJSON`
  result spelling it keeps it, and only an invalid byte is refused.
- **`codec.UnmarshalWithUnknown` keeps a member matching a field only when case is
  ignored, in both builds.** The `encoding/json` build refused it.
