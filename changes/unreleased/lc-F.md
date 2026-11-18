### Changed

- **The generators read the job views in `types`.** The `magus-utils` boundary roots
  `types.Declaration` in place of `job.Declaration`, so Buzz keeps `magus\LeaseCheck`
  and the typed `check`. `jobschema` keys structs by package and applies the same
  field-visibility rule as buzzgen, so a view's property replaces the embedded one in
  place and the module's version constant is still read.
