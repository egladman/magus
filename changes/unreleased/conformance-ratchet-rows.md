### Changed

- **Breaking: the conformance ratchet is a JSON Lines file.** `knowledge.conformance.ratchet` names a file with one JSON object per line, a `schema`, `measured`, `threshold` or `known` entry. An unknown kind or member, a repeat, a wrong or missing value and a key committed twice are errors naming the file and line. A single JSON document is refused; rewrite it as lines.
