### Changed

- **The conformance ratchet is a JSON Lines file.** `knowledge.conformance.ratchet` names a file with
  one JSON object per line, each a `schema`, `measured`, `threshold` or `known` entry, in place
  of a single JSON document, so `grep` and `diff` read it. Decoding is strict: an unknown
  kind or member, a repeated member, a wrongly typed or missing value, a repeated line and a
  key committed twice are errors naming the file and line. A single-document record is refused
  with no fallback reader; rewrite it as lines (see the `conformance` lens in the insight guide for
  the members).
