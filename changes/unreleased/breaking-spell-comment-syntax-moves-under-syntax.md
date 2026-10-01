### Changed

- **Breaking (spells): comment syntax moves under `Language.syntax`.** A spell writes
  `syntax = Syntax{ comments = CommentSyntax{...}, stubs = StubSyntax{...} }` where it
  wrote `comments = CommentSyntax{...}`.
