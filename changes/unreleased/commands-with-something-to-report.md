### Changed

- **Thirty commands that report an outcome now take every `-o` format.** Among them
  `config set`, `import`, `agent install`, `update`, `graph build`, `pull`, `push`, `init`
  and `queue ls` print a record under `json`, `yaml`, `jsonl`, `name` and `template`.
  Twenty-two commands still refuse `-o`, because their stdout is a document, a stream or
  a secret.
