---
title: "ADR 0061: a code template is a value with typed holes"
order: 61
description: A Buzz host module fills target-language template files the way PEP 750 defines a template, with static strings and interpolations and no logic in the text. Every hole names an encoding; the language's spell declares ident and string as data, and block and raw are structural. A template file declares its hole delimiter on its first line. The result is planned as new files through splice's engine, previewed as a diff and applied as a whole. Nothing is owned, formatted or pruned.
tags: [adr, decision, buzz, codegen, templates, splice, spells]
status: accepted
date: 2026-11-26
---

# ADR 0061: a code template is a value with typed holes

## Context

Measured at 8cd093953 on 2026-11-25.

- An agent writing several similar files writes each by hand or through a shell heredoc.
  In a heredoc every `$` and backtick of the target is escaped by hand, and a value with
  a quote in it breaks the file with no error.
- A Buzz backtick string is no better home for target text. `` `[]int{1}` `` prints
  `[]int1`, and whether a brace group survives depends on what is in scope.
- `template\render` is Mustache. It writes `a &lt; b` for `a < b`, writes nothing for an
  absent key and consumes the `{{A: 1}}` of a Go composite literal.
- Helm shows where logic in template text ends: indentation computed by hand
  (`indent 4`), a helper library, and templates nobody can read as the file they produce.
- Python's PEP 750 (Final, 3.14) specifies the smallest model that avoids all of this: a
  template is a value holding static strings and interpolations, it is not a string, it
  does not join to one, it nests, and each interpolation carries a spec that the
  processing code interprets.
- An earlier draft proposed generator ownership: declared outputs, a drift verdict, a
  formatter contract, prune. Review found twelve blocking problems in it. The scope was
  then set to ad hoc writing of code a person then owns.

### Evidence

- PEP 750, read in full on 2026-11-25: https://peps.python.org/pep-0750/
- C# interpolated raw string literals, for the counted delimiter:
  https://learn.microsoft.com/en-us/dotnet/csharp/language-reference/tokens/interpolated
- Literal and Mustache reproductions: `./magus buzz`, 2026-11-25, rerun by the review.
- Backtick inventory: 596 backtick strings in 116 `.buzz` files at 8cd093953; 464 depend
  on gopherbuzz's lenient reading (heuristic scan, not yet checked by BZZ3006).
- Review of the first draft: 52 findings, 12 blocking.

## Decision

| behavior                                                                                                                                                                                                                                                                                    | enforcement                                                                | State   |
| ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | -------------------------------------------------------------------------- | ------- |
| A template is `Template{strings, interpolations}` with `Interpolation{value, expression, conversion, formatSpec}`, as PEP 750 defines them. It is not a `str`.                                                                                                                              | `types/emit.go`; the Buzz checker                                          | planned |
| A template comes from a file (`emit\load(path, values:)`) or from `emit\text` and `emit\hole`. No function parses holes out of a `str`.                                                                                                                                                     | the module's signatures; `std/gen/std-api.lock`                            | planned |
| Template text holds static text and holes. No section, loop, branch, filter or helper exists. Control flow is Buzz; repetition is `emit\join(parts, sep:)`; composition is a template in a hole.                                                                                            | the grammar has one production; parser tests                               | planned |
| A template and a `str` never join. A `str` enters as static text or as an encoded hole, by a call that says which.                                                                                                                                                                          | the Buzz checker; BZZ3005                                                  | planned |
| Line 1 of a template file is the hole form with the brace count the file uses, `{{hole}}`. Shorter brace runs are text; longer runs group from the inside out; a run twice as long is refused. There are no escapes.                                                                        | MGS1064, MGS1065                                                           | planned |
| A hole is one name, an optional conversion `r`, `s` or `a`, and a spec. `values` names every hole and nothing else. A null is refused.                                                                                                                                                      | MGS1066, MGS1068 to MGS1070                                                | planned |
| Every hole names an encoding. An empty spec is refused.                                                                                                                                                                                                                                     | MGS1071                                                                    | planned |
| `ident` and `string` are declared by each language's spell as data in `Syntax.encodings`, routed by the output path's extension. The shipped spells for Go, TypeScript, Python, Rust, shell and Buzz declare them, and so do YAML, JSON, TOML and Markdown. A spell may declare more names. | spell contract validation (MGS1051); MGS1072, MGS1074                      | planned |
| `block` and `raw` are structural and belong to the engine. `block` takes a template and repeats the hole line's leading whitespace on every line; `raw` takes one line of text exactly. No Go code names a language.                                                                        | MGS1073, MGS1076; a test that the engine holds no quote, keyword or escape | planned |
| `emit\plan(files)` renders every file and resolves each as a new file through `internal/edit`, returning the plan type splice returns. `splice\diff` previews it and `splice\apply` writes every file or none.                                                                              | splice's engine, unchanged                                                 | planned |
| The only change kind `emit` constructs is `create`. `types.EditChange` gains no field and `internal/edit` gains no kind.                                                                                                                                                                    | a test in `internal/emit`; the API lock                                    | planned |
| A path that exists with other bytes is refused. A second run with the same input writes nothing.                                                                                                                                                                                            | splice's `create` rule, MGS1063                                            | done    |
| Nothing is formatted, pruned, marked as generated or declared as an output. The workspace's format check judges the files like any others.                                                                                                                                                  | absence; the refusals record                                               | planned |
| If generator ownership is built later, its kinds are fields of the internal `edit.Request`, and its plan is a type `splice\apply` does not take.                                                                                                                                            | review; this record                                                        | planned |
| `template` stays Mustache, for HTML. Its doc says so and names this module for code.                                                                                                                                                                                                        | `std/template.go` doc; a repo lint                                         | planned |

The module is `emit`, its package is `internal/emit`, and it is a bare Go host module
beside `splice`. It reads the spell language index and the lease from the run, and with
neither on the context it refuses rather than render unencoded.

Differences from PEP 750, each forced by Buzz: there is no template literal, so a file is
parsed and bound in one call (the PEP's `from_format` route); a hole's expression is one
name; the delimiter is a declared brace count because the text is a file of target code,
not a Python literal; the renderer honors `!s` and refuses `!r` and `!a` because Buzz has
neither `repr` nor `ascii`; a spec cannot hold a hole and `=` is refused; `Template`
carries `source` so a refusal can name the file; concatenation is `join` with an empty
separator because Buzz has no operator overloading; failures are Result values.

### Principles checked

- [Optimize the loop that verifies](../design/principles/optimize-the-loop-that-verifies.md):
  this decision does not shorten a verify step and does not claim to. It makes text an
  agent was already going to write arrive correct, and it shows the diff before the
  write. It is recorded in [strained](../design/strained.md) as a decided exception. The
  principle page is unchanged. Its bad example stands: magus ships no template and gains
  no generate verb.
- [No generated code in the build path](../design/principles/no-generated-code-in-the-build-path.md):
  "Scaffolding hands you a file and leaves." Every file this writes is the person's at
  once; nothing has to exist for a build to work, and nothing is kept in step with a
  magus version.
- [What we do not build](../design/principles/what-we-do-not-build.md): the refusals
  below are recorded in the same change.
- [Capability is not a reason](../design/principles/capability-is-not-a-reason.md): each
  failure removed has a reproduction in Context.
- [Delete before you add](../design/principles/delete-before-you-add.md): no plan type,
  diff, apply, change kind, formatter contract or drift verdict is added. The module is a
  parser, a renderer and one call that builds `create` changes.
- [One source, every door](../design/principles/one-source-every-door.md): one write
  engine; a language's lexical data stays in its spell beside its comment syntax.
- [Buzz modules live in their layer](../design/principles/buzz-modules-live-in-their-layer.md):
  a Go host module beside `splice`. The layer page gains one sentence covering host
  modules that read the run.
- [The checker catches it](../design/principles/the-checker-catches-it.md): a template
  is not a `str`, so joining one to text and passing one where text is declared fail at
  check time. A template's content is a file read at run time, so its refusals are
  runtime ones and each says so. Matching a literal path against a literal `values` map
  at check time is planned and is a debt until built.
- [No silent defaults](../design/judgment/no-silent-defaults.md): no default encoding, no
  fallback to `raw`, no guessed delimiter, no empty render of an absent value.
- [A refusal carries its reason](../design/judgment/a-refusal-carries-its-reason.md):
  every refusal names the template file, the line and the edit.
- [Told, never guessed](../design/judgment/told-never-guessed.md): the delimiter is on
  line 1, the encoding is in the hole, and the language is the output path's extension.
- [Behavior is code, facts are data](../design/principles/behavior-is-code-facts-are-data.md):
  an encoding is a list a spell declares; one engine reads every language's.
- [Clear is better than clever](../design/principles/clear-is-better-than-clever.md):
  one production in the grammar; indentation is copied, never computed.
- [A copy can be the right answer](../design/principles/a-copy-can-be-the-right-answer.md):
  this is not Mustache with switches. It shares no semantics with `template` (escaping,
  lenient lookup, sections), so the two stay separate and each says what it is for.

### Enforcement

- `internal/emit` tests pin every refusal by code, one case each.
- A test asserts every change an `emit` plan carries is a `create`.
- A test asserts `internal/emit` holds no quote character, keyword list or escape table.
- `std/gen/std-api.lock` holds the module's signatures and `EditChange`'s shape.
- Spell contract validation refuses a malformed encoding when the spell loads; each
  shipped spell's test encodes a quote, a backslash, a newline, a control and a keyword.
- buzz-lint BZZ3005 reports a template inside a string interpolation.
- Repo lints in `hack/lint`: `template\render` reaching a code file; a count of
  `emit\text` and `:raw`.
- Debts recorded in [strained](../design/strained.md): the verify-loop exception; the
  check-time match of a template's holes against a literal `values` map; inner lines of a
  multi-line raw string inside a `block` value are indented.

## Alternatives

### Mustache with strict options

The library has switches for missing keys and raw output, and a set-delimiter tag. Set
aside: it still has sections and partials, so logic lives in template text; a hole
carries no encoding; and its result is a string, so fragments are joined as text.

### Go `text/template`

Set aside: pipelines, functions, `range`, `with` and `if` in template text are the
language this decision exists not to have. `missingkey=error` fixes one hazard of five.

### Sections, loops or conditionals in the template

Set aside. Buzz already has `foreach` and `if`, typed and checked. A second, untyped
control language inside the text is how a template stops reading as the file it makes.
PEP 750 has none, and its authors expect "nesting and composition" in their place.

### JavaScript tagged templates

Set aside for the reasons PEP 750 gives: an open namespace of tag functions is "too
complex to build in full generality". A tagged template also has no per-hole slot, so
every encoding would be a wrapper call around a value in Buzz.

### A new template language

Set aside. The grammar here is one production: delimiter, name, optional conversion,
spec. Anything larger is a language to specify, teach and keep.

### A verbatim string literal in Buzz, now

Set aside. gopherbuzz matches upstream Buzz, which has no such literal, and this decision
involves no upstream change. A file is also the better home for anything longer than a
line: it opens in an editor and reads as the target.

### A default encoding for an unencoded hole

Set aside. No one encoding is right for a name, a literal and verbatim text. A `raw`
default would be a heredoc with more steps.

### Non-ASCII delimiters (`«name»`)

Set aside. They are ordinary quotation marks in several languages, are not on a US
keyboard, and a look-alike glyph would render as text with no refusal.

### Generator ownership: declared outputs, drift verdict, formatter, prune

Not in this decision. Review found it would add a third drift verdict, a second
description of how a project formats, and delete paths another writer owns.

### Typed builders, or translating Buzz to a target language

Set aside on 2026-11-09 and again here: one builder per language puts languages in core.

## Consequences

- An agent writing several similar files gets a value whose holes are encoded by name, so
  a quote in a value cannot break the file silently, and the diff is shown before the write.
- A second run with the same input writes nothing, and a path that already exists with
  other bytes is refused. Replacing a file is not offered in this decision.
- Every shipped language spell carries an encoding list that its own test exercises, so a
  new language costs a spell declaration and a test, not an engine change.
- The refusals below are recorded in [refusals](../design/refusals.md) in the same change:
  control flow in template text, a default encoding, Mustache or `text/template` for code,
  generator ownership, a verbatim string literal ahead of upstream, translating Buzz or
  typed builders, and a `magus generate` verb or shipped templates.

## Open questions

Each item below is unverified or unmeasured. Each has a named check in the build plan.

- Whether a host module can reach the resolved spells' language list from the context of a
  `magus buzz` script. `changeClassifier` in `magus.go` builds the comment index from the
  resolved spells, and no `std` accessor exposes it yet.
- Whether generated declarations can name `splice\SplicePlan` from the `emit` module. If
  not, `emit` declares an identical plan object, or returns `[EditChange]` from
  `splice\plan`.
- Whether "less brittle than a heredoc" and "intuitive" hold. No trial exists yet; a
  routine-tier trial comes before any guard remedy names the module.
- The Buzz in the design's worked examples has not been checked; map and object syntax may
  need correction when the examples are written for real.
- Declaring the delimiter count on a file's first line has no precedent; C# and Kotlin put
  it on the literal. The inside-out grouping rule follows C#, and the bound of twice the
  count for a refused run is this design's reading, not quoted text.
- The declarative `Encoding` shape was designed against Go, TypeScript, Python, shell and
  YAML double-quoted strings. Rust, Python's triple-quoted and raw forms, and TOML were not
  worked through, and `Encoding` is a public spell contract.
- `block` indents inner lines of a multi-line raw string or heredoc inside the nested
  template. This is not solvable without parsing the target.
- What typos, proofread, the license-header check and `.gitattributes` do with `*.tmpl`
  files was not checked. Ad hoc templates can live outside the repository, which avoids it.
- Created files take whatever mode `splice\create` gives, so an executable script is not
  offered.
- An agent under a job lease needs write paths covering every output, and the existing
  `new-file` guard rule grades each created file. Whether N creates in one plan read well
  in that rule's message was not checked.
- The unused-value refusal (MGS1069) is stricter than `str.format`, which ignores extra
  arguments. It blocks sharing one `values` map across two templates.
- `ident` validates a value alone, so a legal joined name can be refused (`Test` joined
  with `type`). This is over-refusal only, with no silent wrong output.
- ADR number 0061 may be taken by an open branch before this merges, in which case the
  file, title, `order` and links are renumbered at merge.
