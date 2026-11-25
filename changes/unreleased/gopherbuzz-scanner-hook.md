### Changed

- **gopherbuzz takes a host literal scanner, off by default.** `token.NewScanner` builds a
  `*token.Scanner` from `token.LiteralScanner` forms, and `buzz.WithScanner` and
  `buzz.Parser{Scanner, Cache}` lex with it. With no scanner, gopherbuzz lexes upstream
  Buzz. The parse and bytecode caches key on the scanner.
- **Breaking: gopherbuzz calls the backtick form a multiline string, as upstream does.**
  An unterminated one reports `buzz: unterminated string at line L:C`, not
  `unterminated raw string`.
- **Breaking for SDK callers: `token.Token.Raw` is `token.Token.At`.** It marks a free
  identifier, `@"..."`, after upstream's `atIdentifier`.
- **Breaking: gopherbuzz refuses `r"x"` as upstream does.** A string literal on the same
  line after a statement with no `;` fails with `expected ';' after statement`, where it
  read as a second statement.
