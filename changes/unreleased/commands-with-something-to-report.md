### Changed

- **Thirty commands that report an outcome now take every `-o` format.** Among them
  `config set`, `import`, `agent install`, `update`, `graph build`, `pull`, `push`, `init`
  and `queue ls` print a record under `json`, `yaml`, `jsonl`, `name` and `template`.
  Twenty-two commands still refuse `-o`, because their stdout is a document, a stream or
  a secret.

### Fixed

- **`job run` no longer ignores `-o`.** It prints a record of whether the job was submitted,
  coalesced, refused or had no server to go to.
- **The test that pins the `-o` contract can no longer pass a command on a sibling's printer.**
  It finds a handler by the dispatch that calls it, not by a similar name.
