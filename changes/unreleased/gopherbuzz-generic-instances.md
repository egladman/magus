### Fixed

- **Buzz type-checks a generic object's type arguments.** A field of `Result::<int>`
  is an `int`, and returning a `Result::<str>` where `Result::<int>` is declared fails
  with BZZ1005, as upstream Buzz rejects it. Before, the arguments were erased and
  either mistake passed `--check`.
