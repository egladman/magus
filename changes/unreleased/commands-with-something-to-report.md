### Changed

- **Thirty commands that report an outcome now take every `-o` format.** Including
  `config set`, `agent install`, `graph build`, `pull`, `push`, `queue ls` and
  `broker stop`. Twenty-two commands still refuse `-o` with exit 2, because their
  stdout is a document, a stream or a secret.
