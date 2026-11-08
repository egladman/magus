---
title: "ADR 0033: a kind is a reader and its rules"
order: 33
description: Proofread's kinds stop being a closed list that rules and readers branch on by name. A kind becomes a value, a reader with named properties and the rules it judges with their decisions, so the built-in kinds and a team's own kinds are the same thing, declared the same way.
tags: [adr, decision, proofread, kinds, configuration]
status: proposed
date: 2026-11-08
---

# ADR 0033: a kind is a reader and its rules

## Context

A kind says what a text is for, and proofread judges a text by its kind. Today the
kinds are a closed list in `libs/proofread/proofread.go`, and what each kind means is
spread through the code that judges it:

- 20 kinds, each a `Kind` constant, parsed by `ParseKind` against that list.
- 326 `on:` lists, one per check, name the kinds a rule judges. They are built from 25
  shared kind sets (`written`, `published`, `reviewText`, `pageKinds` and the rest), so
  the answer to "which rules judge a changelog" is spread over every family file.
- 51 judges and readers branch on a kind's name, such as `in.kind == KindChangeDescription`
  in `voice.go` or `kind == KindCommitMessage` in `metrics.go`. Each is a fact about the
  kind (it has a title line, it reads no headings, it is sectioned) written as an `if`
  in a rule.
- `judgeText` picks a reader with a `switch` on the kind.

So a team cannot add a kind without forking proofread. A text that is not a page, a
commit or a pull request, such as a list of short standalone entries a team wants held
to one consistent phrasing, has no kind, and the only way to get one is to add a
constant and edit the `on:` lists of every rule it should meet. The same edit is the
only way this repository adds a kind, so every new kind puts business logic into the
library.

## Decision

A kind becomes a value of one type, `KindSpec`, and the library's built-in kinds and a
decisions table's declared kinds are both `KindSpec`s.

```go
type KindSpec struct {
    Name    Kind
    Extends Kind              // a declared kind starts from this kind's spec
    Reader  Reader            // how text becomes input, with its properties
    Rules   map[Rule]Decision // each rule this kind judges, and its default
    Minimum *int              // the grade a text of this kind must reach
    Terms   map[string]string // spellings this kind replaces, as RuleTerms reports them
}

type Reader struct {
    Format   Format // markdown, plain, doc-comment, stylesheet, template, message, lines
    Titled   bool   // the first line is a title, a paragraph of its own
    Headings bool   // headings are read as headings
    Sections bool   // headed sections take their own rules, as a description's do
}
```

| Item                                                                                                                       | State     |
| -------------------------------------------------------------------------------------------------------------------------- | --------- |
| `KindSpec` and `Reader` types; the 20 built-in kinds as `KindSpec` values in one file of the library                       | not built |
| A check declares the formats its judge can read (`reads []Format`) in place of `on []Kind`                                 | not built |
| Every `kind ==` branch in a judge or reader reads a `Reader` property or a spec field instead                              | not built |
| `judgeText` picks the reader from `spec.Reader.Format`; the `switch` on kind is deleted                                    | not built |
| An equivalence test: every built-in kind judges the same rules at the same defaults as before, over every calibration case | not built |
| A decisions table's `kinds` object declares kinds; `proofread <name>` resolves a declared name after the built-ins         | not built |
| A declared kind naming a rule its format cannot feed, an unknown rule or an unknown base is an error naming the field      | not built |
| The `lines` format: each non-blank line or list item is one unit, with no title and no headings                            | not built |
| `Terms` per kind, merged over the built-in glossary, so a kind can hold its own phrasing                                   | not built |
| The rule pages' decision tables are generated from the specs                                                               | not built |

A declared kind in a decisions table looks like this, and is a fact, never an algorithm:

```json
{
  "kinds": {
    "entry": {
      "extends": "release-notes",
      "reader": {"format": "lines"},
      "rules": {"tense": "deny", "claim": "deny", "buzzword": "deny"},
      "minimum": 90,
      "terms": {"utilize": "use", "spearheaded": "led"}
    }
  }
}
```

New judging behavior stays code: a Go rule in the library, or an outside judge as the
repository policy already runs (`OUTSIDE_JUDGES` in `hack/policy/pull_requests.buzz`).
A declared kind only chooses among rules and sets their decisions, so it cannot grow
into a language.

### Principles checked

- [Behavior is code, facts are data](../design/principles/behavior-is-code-facts-are-data.md):
  a kind is facts (which reader, which rules, what decisions, which spellings), so it moves
  to data; the judges stay Go. A declared kind gets no pattern language, which is the
  failure that principle names.
- [Delete before you add](../design/principles/delete-before-you-add.md): the 326 `on:`
  lists, the 25 kind sets and the 51 name branches are deleted, not wrapped.
- [The checker catches it](../design/principles/the-checker-catches-it.md): the built-in
  specs are Go values, so a misspelled rule fails to compile, and a declared kind is
  validated when the table loads.
- [One source, every door](../design/principles/one-source-every-door.md): the CLI, the
  guard's decisions table and the generated rule pages all read the same specs.

## Alternatives

- Add the missing kind as a constant, as every kind so far was added. It lost because each
  such kind puts one team's text into the library, and the next team's needs another
  release. Evidence: the 326 `on:` lists above are the cost of that path. Reopen if no
  second kind is ever wanted.
- Ship the built-in kinds as an embedded JSON table instead of Go values. It lost because
  a typo in a rule name would surface at load time instead of at compile time, and the
  graph could not resolve a rule's kinds through `magus refs`. The user's table and the
  built-ins still decode into one type. Reopen if a non-Go consumer must read the built-ins
  without running proofread.
- Let a declared kind define rules with regular expressions, as Vale's styles do. It lost
  because a rule needs exemptions (quoted words, code spans, mentions) that a pattern list
  either reimplements or gets wrong; `claim`, with Go behind it, measured 51.1% precision
  before its exemptions and 74.2% after, in labeling round 2 (2026-10-11). Reopen if outside judges prove too slow to write.
- Keep `on:` and add declared kinds beside it. It lost because two ways to say which rules
  judge a kind drift, and the built-in kinds would still branch by name.

## Consequences

- Adding a kind to proofread is a `KindSpec` value, with no change to any rule.
- A rule that needs a fact about a kind reads it from the spec, so the fact has one name
  a declared kind can set.
- A team keeps its own kinds in its own decisions table, outside the library and outside
  this repository.
- The migration is mechanical but wide: every check and 51 branches change in one series,
  proven by the equivalence test before the old lists go.

## Open questions

- Whether `Sections` and `Titled` cover every name branch, or a third property is needed;
  the migration lists each branch and the property it became.
- Whether a declared kind may set a rule's weight, or only its decision.

## Amendments
