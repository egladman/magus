### Changed

- **Breaking for SDK callers: the test doubles follow the standard library's `<pkg>test`
  idiom.** `spells/gen/mocks` is `spells/gen/spellstest`, `std/gen/mocks` is
  `std/gen/stdtest` and `project/impact/gen/mocks` is `project/impact/gen/impacttest`,
  each in a package of that name. `types/gen/mocks` is gone from the public API: its
  doubles are internal, and a caller fakes a `types` role by embedding the interface or
  with a mock it generates itself.
