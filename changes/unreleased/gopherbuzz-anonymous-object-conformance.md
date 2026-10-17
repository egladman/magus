### Fixed

- **gopherbuzz checks a `.{ ... }` literal against its `obj{ ... }` type as upstream Buzz does.** A `.case` inside it, returned as or assigned to that type, resolves against the field's enum. The literal must name exactly the declared fields, each of a compatible type, so `.{ tier = "high" }` for a `tier: Tier` field fails the check.
