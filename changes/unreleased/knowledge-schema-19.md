### Changed

- **The knowledge schema is 19.** A file node records `generated` when its source opens
  with a `// Code generated ... DO NOT EDIT.` header, and a symbol records its body
  shape. A graph built at an earlier schema is read as absent and rebuilt in full on the
  next `magus graph build`. Nothing needs migrating.
