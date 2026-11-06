### Fixed

- **`ns\Type{...}` builds ns's own type.** An object literal qualified by an aliased import
  was built against whatever type held the bare name. When the host had bound one first,
  as magus binds `Run`, every field the literal set read back null.
