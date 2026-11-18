### Changed

- **buzzgen follows Go's field-visibility rule.** An embedded field hidden by a
  shallower field of the same name is no longer mirrored, and the shallower field takes
  the hidden one's place. Two embedded fields of one name at one depth are ambiguous
  and neither is mirrored. `buzzgen.MirroredFields` is exported and `emit.ObjectFields`
  reads it.
