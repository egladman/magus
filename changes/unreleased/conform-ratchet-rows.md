### Changed

- **The conform ratchet is a plain file of tab-separated rows.** `knowledge.conform.ratchet`
  names a file with one `threshold` or `known` row per line, a `schema` row and optional `#`
  comments, in place of a JSON document, so `grep` and `diff` read it. Parse errors name the
  file and line. A JSON record is refused as an unknown row kind with no fallback reader;
  rewrite it as rows (see the `conform` lens in the insight guide for the columns).
