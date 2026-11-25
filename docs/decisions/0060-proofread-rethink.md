---
title: "ADR 0060: the proofread rethink"
order: 60
description: proofread's rethink starts with slice 1, a shared document model and pattern engine that ports the six worst-measured rules. Each person has one voice, voice files never reach a repository, a user profile may declare pattern rules, proofread applies safe fixes itself, and code comments share one budget everywhere.
tags: [adr, decision, proofread, voice, rules, comments]
status: accepted
date: 2026-11-24
---

# ADR 0060: the proofread rethink

## Context

proofread's findings contract, decisions table, kinds as values, frozen labeling
rounds and voice statistics hold up. Its middle does not: about 100 rules each
re-read the text with their own regexes and exemption switches, with no shared
tokens. The rules with the lowest measured precision are all word lists and regexes:
`absolute` scores 6 of 40, `buzzword-weak` 10 of 40, `verdict` 16 of 40 and `buzzword`
26 of 40 in `evidence.go`.

The labeled cases are the asset a rewrite from nothing would throw away, so the port
goes rule family by rule family against them, inside `libs/proofread`, with
`JudgeText`, the findings JSON, PRF codes and the CLI unchanged until the last slice.

A voice is personal. It holds an author's habits and preferred words, and a file that
names them does not belong in a repository that others read.

Agent-written code comments ran long, defended changes and narrated. magus's exported
docs ran to 83 words at p90 against 74 in the Go standard library, and 29% held a
sentence over 25 words against 11.6% there.

## Decision

| behavior                                                                                                                                                                                                                       | enforcement                                    | State   |
| ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ | ---------------------------------------------- | ------- |
| Slice 0 records every rule's firing set over the labeled cases and round samples, and `BenchmarkJudgeText` per kind, with no behavior change.                                                                                  | the slice 0 oracle                             | planned |
| Slice 1 comes first: one `Document` per text with shared tokens and tags, the pattern engine, and `intent`, `blame`, `verdict`, `absolute`, `buzzword` and `buzzword-weak` ported onto it. `Shape` moves onto the same tokens. | the slice 0 oracle and a fresh round           | planned |
| Slice 1 is done when every changed firing on the labeled cases is listed and labeled, a fresh frozen round scores the six rules, and the benchmark is no slower.                                                               | a fresh frozen round                           | planned |
| The later slices merge in any order, as long as all of them merge.                                                                                                                                                             | review                                         | planned |
| Each person has one voice. There is no team voice.                                                                                                                                                                             | the voice file schema                          | planned |
| A voice file may hold the author's preferred and avoided style words. It holds no names, identifiers or paths, is never committed, and never leaves the author's machine.                                                      | `voice build` refuses a work tree              | planned |
| A user profile may declare pattern rules, at user level only. A repository's table declares none.                                                                                                                              | the profile reader                             | planned |
| proofread applies a fix marked safe itself, under `proofread fix --safe`.                                                                                                                                                      | the CLI                                        | planned |
| A code comment has one budget everywhere: trailing 12 words on 1 line, inside a body 30 on 3, an unexported doc 40 on 4, an exported doc or file header 60 on 8, and a sentence 25 words.                                      | `code-comment-budget`, `code-comment-sentence` | done    |
| Blank `//` lines do not count toward a comment's lines, and there is no column rule.                                                                                                                                           | the `code-comment` kind                        | done    |
| A project that proves it needs a larger comment budget names a `zones:` entry in its own `analyzers.yaml`, with evidence. Until then a project may only tighten the budget.                                                    | the project's `analyzers.yaml`                 | planned |
| The `code-comment` kind is absorbed into the `Document` model: its scanner becomes a reader, its budgets measure rules and its wording rules pattern rules. `watchdog.go` is its equivalence fixture.                          | the `watchdog.go` fixture                      | planned |

A pattern rule is data in a closed vocabulary: token predicates, a skip, a scope,
`unless` context predicates and a fix template, with no variables, loops or
arithmetic. What it cannot say is a Go rule. That bound is what lets a user declare
one without a repository learning a language.

A user profile adds and never loosens: inside a repository the repository's decisions
hold. A user-origin rule or kind never prints its name into a file written into a
work tree.

A comment rule that does not flag `watchdog.go` does not ship. That file started the
comment cleanup at 36% comment lines.

### Principles checked

- [A kind is a reader and its rules](0033-a-kind-is-a-reader-and-its-rules.md) gives
  a declared kind no pattern language. This decision reopens it at user level only,
  bound by the closed vocabulary, and the commit that builds user pattern rules
  amends ADR 0033.
- [proofread judges a text against its author](0037-proofread-judges-a-text-against-its-author.md):
  style words in the voice file depart from "the voice file holds no content word",
  bound to user level and to style words. The commit that builds them amends ADR 0037.
- [No silent defaults](../design/judgment/no-silent-defaults.md): a finding names the
  layer that set its decision or budget, and a project's extra comment budget is a
  named zone, not a knob.
- [A file a person edits is YAML](../design/principles/a-file-a-person-edits-is-yaml.md):
  the user profile and a project's comment zones are YAML.

## Alternatives

Rewrite proofread from nothing. The labeled cases and rounds are what make its rules
measurable, and a rewrite would have to rebuild them before it could say a rule got
better.

A team voice. A team's text blends its authors, so the model would flag each author
against an average none of them writes.

Commit voice files beside the code they judge. A voice holds personal style words and
would publish them to every reader of the repository.

A comment budget per path or per public package. A knob per path drifts toward the
longest comment anyone wrote; one budget with a zone that carries evidence keeps the
exception visible.

## Consequences

The worst-measured rules move first, so the first fresh round shows whether the
pattern engine raises precision where it is lowest.

A user can write private pattern rules and keep a voice, and no repository sees
either.

`proofread fix --safe` edits text in place, so a fix marked safe has to be one a
reader would accept without looking.

Comment budgets deny as house rules before a labeled calibration round measures them;
that round is still owed.
