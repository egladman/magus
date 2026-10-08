# conventions

Go analyzers for this repository's own rules over Go source. Each one was a test
in the root `conventions_test.go` that walked the tree as text; as an analyzer it
runs in `magus run lint` beside the stock linters, reports at the offending line,
and takes a `//nolint:<name> // <reason>` where an exception is deliberate.

| Linter          | Reports                                                                                                     |
| --------------- | ----------------------------------------------------------------------------------------------------------- |
| `hostagnostic`  | a line of non-test source naming an agent host outside a filesystem path                                    |
| `hostvocab`     | a host's tool name (`"Read"`, `"Bash"`) as a string literal in guard code                                   |
| `ruletext`      | guard rule text (`"magus workspace:"`) in the guard's CLI half                                              |
| `asciistrings`  | a typographic glyph in a string literal of a listed user-facing file                                        |
| `importceiling` | a package importing more packages under a prefix than its ratchet allows                                    |
| `stutter`       | an exported package-level name opening with its package's name                                              |
| `filenames`     | a Go file name segment that splits into two segments the tree uses as file names                            |
| `nameoutput`    | a `case outputName:` arm that does not render through an emitter                                            |
| `testisolation` | a test binary linking the runtime-directory package with no isolating `TestMain`                            |
| `fieldwise`     | a test asserting every field of a struct one at a time instead of comparing the whole value once            |
| `providerio`    | Go source outside an allowlist reaching toward a CI/VCS provider (an HTTP client, or a provider SDK import) |

Every path, word list, host name, ceiling and exemption lives in the root
`.golangci.yml`, so the analyzers carry the mechanism and the config carries the
policy. No diagnostic names this repository's identifiers or paths on its own: an
analyzer whose remedy is repository-specific takes a `hint` setting, appended to
each of its diagnostics.

## Scope

golangci-lint stops at a `go.mod`, so the root `lint` target runs it once more in
each module under `libs/`, on the same config, with only `hostagnostic`,
`stutter` and `filenames` enabled: the rules the old tree walks held everywhere.
The rest name root-module paths.

`filenames` reads beyond its package on purpose: its vocabulary is every Go file
name the go tool would read under the root of `module`, nested modules included,
walked once when the plugin loads. Like the go tool, the walk never enters a dot
or underscore directory or `testdata`. Rules over source text also read the files
a package's build constraints exclude on this platform, so a darwin run still
checks the `_linux.go` files the walks did.

A setting that names a path (`files`, `package`, `dirs`, `skip-dirs`) is checked
against the tree when the plugin loads, from the directory whose `go.mod`
declares `module`. A pattern matching no file, a package or directory with no Go
files, or a skip entry naming no directory that holds Go files, is a load error
naming the setting: a scope that matches nothing would otherwise report nothing,
and a skip entry that matches nothing is a setting the tree moved out from under.

`stutter` reads the package name from the package clause, which is what a call
site spells, and skips `package main`, which has none. Its zero settings check
every package name of three bytes or more.

`testisolation` carries reach as a package fact along imports, which needs type
information; the rest load types only because golangci-lint leaves the package
unset without them.

`fieldwise` reads test files only. Its zero settings report a value whose
assertions name every field its struct declares, which one comparison of the
whole value replaces exactly. `report-partial` adds a value asserted on two or
more of its fields: that fix compares fields the test never looked at, so it may
need volatile fields normalized first. This tree turns it on: once a struct grows
a field, a test that named every field asserts only some, and only
`report-partial` still reports it.

## Not here

Rules whose subject is not one package's Go source stayed tests: a vocabulary or
symbol index built from the whole tree (comments naming symbols that exist), and
rules that also read Buzz, shell or markdown
(environment sniffing, registered env vars, diagnostic raise sites, rescanning
string loops).

## prose

`prose` is not an analyzer, and golangci-lint never loads it. It judges four
kinds of text: one symbol from a SCIP index (the doc comment, and the name of a
function or method), a hand-written Markdown file, a skill, and a pull
request's title and description. Its rules are this repository's policy, not magus's: the magus
module never imports `libs/conventions`. `cmd/judge-docs` runs them. With no
flag it reads the symbols of `magus\symbols()` as JSON on stdin, fed by
`hack/lint/symbol-docs-follow-prose-rules.buzz`, and a finding points at the
declaration because an index records no position inside a doc. With
`-surface markdown` it judges the files its arguments name, and with
`-surface pull-request` a pull request on stdin, the title on the first line;
each finding names its file and line. `-surface skill` judges a SKILL.md an
agent loads as written, and `-surface skill-source` a skill body
`internal/agent` renders with `text/template`: what its short form shows meets
the skill rules, and what only its full form shows meets the Markdown ones and
`bare-rule`. `-surface guide` judges a procedural page (`docs/guides/`,
`docs/setup/`, `docs/migrating/`) on the Markdown rules and the guide rules.
It writes the findings as JSON on stdout.
A Go symbol and a TypeScript one meet the same rules. `prose` imports only the
standard library.

| Rule               | Surfaces     | Reports                                                        |
| ------------------ | ------------ | -------------------------------------------------------------- |
| `comment-block`    | doc          | a doc over 250 words                                           |
| `comment-sentence` | doc          | a doc sentence over 60 words                                   |
| `filler`           | all          | throat-clearing ("Note that") and filler adverbs ("simply")    |
| `terms`            | all          | a spelling the glossary replaces ("sub-agent")                 |
| `name-suffix`      | doc          | a function or method name whose last word is Of or For         |
| `aside`            | doc          | a spaced hyphen spelling an em-dash, inline or ending a line   |
| `history`          | doc          | a phrase narrating the change rather than the code ("used to") |
| `docstub`          | doc          | a one-line doc that only repeats the symbol's name             |
| `lead-context`     | pull request | a description that does not open with a paragraph of its goal  |
| `reply-voice`      | Markdown, PR | a reply opener, a conversation, a bold-label list item         |
| `tense`            | Markdown, PR | the future tense, and the author as the actor of a change      |
| `hedge`            | Markdown, PR | a softener on a claim ("might fix", "probably")                |
| `attribution`      | Markdown, PR | credit to a tool, or an account of how the work was made       |
| `terse-sentence`   | skill        | a sentence over 25 words                                       |
| `terse-paragraph`  | skill        | a paragraph or list item over 60 words                         |
| `wordy`            | skill        | a phrase with a shorter equivalent ("in order to")             |
| `bare-rule`        | skill        | "rule" with no mechanism named, in either form of a skill      |
| `second-person`    | guide        | we, us, our or ours where a guide addresses you                |
| `step-verb`        | guide        | a numbered step that opens with no verb ("1. The target...")   |
| `condescension`    | guide        | a word telling the reader a step is easy ("easy", "simple")    |
| `template`         | skill source | a body that does not render, so neither form can be judged     |

A skill and a guide take every Markdown and pull request rule but `lead-context`.
A numbered list is a procedure, and `step-verb` judges it, only when one of its
items opens with an imperative; a recap, a precedence order or a list of reasons
is left alone. `condescension` leaves a word `filler` reports to it, and a
`second-person` "we" that `tense` reports to that rule, so one word gives one
finding.
Markdown and pull requests take a wider `filler` list ("actually", "robust")
than doc comments do. docs/conventions.md states the written rules for authors.

Code in a doc, fenced or indented, is never judged. A backtick span still counts
toward the budgets, but no wording rule reads inside one.
A doc with a line opening in TODO, FIXME, BUG, `compat(until:`, `compat:` or
`Deprecated:` is exempt from `history` and `docstub`.
