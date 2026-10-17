### Changed

- **Breaking: `magus query` ranks free text instead of requiring every term.** A match
  needs any one query word, scored by BM25F over names, signatures, paths and docs;
  partial matches rank among the rest. Field matchers filter as before; a term with a
  path separator must appear whole. `explain`, `path` and `refs` still name a node only
  from a full match.
