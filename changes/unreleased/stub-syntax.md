### Added

- **A spell declares how its language writes a placeholder body.** `mgs_getLanguage`'s
  `Language` record gains `syntax.stubs`: the SCIP symbol kinds a stub may replace, whether
  the body is brace- or indent-delimited, and a Mustache body template with `Name`,
  `Qualified`, `Kind` and `Branch` holes. The go, typescript, rust and python spells declare
  one; an unknown body style or kind is a load error naming the spell.
- **`magus describe spell` carries each spell's language record.** `-o json` adds
  `extensions` and `syntax` (comments and stubs); the text output adds a one-line
  `syntax:` summary.

### Changed

- **Breaking (spells): comment syntax moves under `Language.syntax`.** A spell writes
  `syntax = Syntax{ comments = CommentSyntax{...}, stubs = StubSyntax{...} }` where it
  wrote `comments = CommentSyntax{...}`.
- **Breaking (Buzz): `magus\describe` is an object with one typed method per noun.**
  `magus\describe.file`, `.module`, `.spell`, `.charm`, `.target`, `.evaluatedTarget`,
  `.project`, `.evaluatedProject`, `.graph`, `.graphMarkdown`, `.workspace`, `.tool`,
  `.rule`, `.harness` and `.mcpTool` each return the record `magus describe <noun> -o json`
  prints. They replace `magus\describe(args)`, `magus\describeFile`,
  `magus\describeModule`, `magus\projects`, `magus\targets` and `magus\tools`; a noun
  with no method (`job`) is reached through `magus\cmd("describe", [...])`.
- **Breaking (Go API): `types.Charm` is `types.CharmEntry`,** and the guard's `RuleDoc`,
  the harness plan and the MCP tool catalog records live in `types`.
