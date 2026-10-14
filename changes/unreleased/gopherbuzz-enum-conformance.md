### Fixed

- **gopherbuzz checks anonymous objects and empty enums as upstream Buzz does.** `enum X {}`
  is a parse error, and a `.case` inside a `.{ ... }` literal returned as or assigned to an
  `obj{ ... }` type resolves against the field's enum. That literal must name exactly the
  declared fields, each of a compatible type, so `.{ tier = "high" }` for a `tier: Tier`
  field fails the check. Hosts read enums and object-keyed maps through the new
  `Value.EnumDef`, `Value.EnumCase` and `Value.MapKeyValues`.
