### Removed

- **Breaking: `types.ParseAPIVersion` is removed.** Use `APIVersion(s).Valid()`.
  `Valid` now follows `libs/apiversion`: a dotted group is required, a leading
  digit is allowed, and a trailing hyphen is refused.
