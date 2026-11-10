### Fixed

- **`serialize\jsonDecode` returns the document it was given.** Object members keep their
  document order. A number with a fraction or an exponent decodes as a float, so `10.0` has
  a `.float()` of 10. An integer past 48 bits is refused with
  `Could not parse JSON: `N` is not a number`, as upstream Buzz does, not wrapped.
