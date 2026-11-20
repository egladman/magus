### Fixed

- **The Buzz checker refuses an optional value in a non-optional slot.** `final n: int =
  maybe()` with `maybe() > int?` failed only at run time, with no line. It now fails the
  check, as in upstream Buzz. A generic call that writes no type arguments is also
  checked against the declared return, so a mismatched inferred type is refused.
