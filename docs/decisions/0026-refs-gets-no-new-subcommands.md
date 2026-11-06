---
title: "ADR 0026: refs gets no new subcommands"
order: 26
description: >-
  Which subcommands `magus refs` should have beside reading a symbol's body. The candidates were callers, callees, implementers, a file outline, unused symbols and the subcommand split itself. Each was judged by its use across 198,579 agent shell calls and by whether explain, query or describe file already answers it. None earns a place: the outline and implementers are already answered by explain, callers by refs itself, and the split was refused on 2026-10-31. Nothing is built.
tags: [adr, decision, refs, cli, symbols, knowledge-graph]
status: proposed
date: 2026-11-06
---

# ADR 0026: refs gets no new subcommands

## Context

On 2026-10-31 Eli asked what subcommands `magus refs` should have beside `show`, the
read of a symbol's body. The same day the split into `refs show`, `refs rename`,
`refs grep` and `refs ls` was stopped, then refused, after a study by persona. The
study found that no measured failure was caused by the command's shape. `refs` kept
its modes as flags (`--source`, `--occurrences`, `--text`, `--rename`) and got four
fixes instead. The open question is narrower: does any new question about a symbol
deserve a verb or a flag?

magus lists 36 top-level commands in `magus -h` at `be17ecc08`. The bar for more is
the one [Delete before you add](../design/principles/delete-before-you-add.md) sets.

The evidence is every agent shell call in this repository's Claude Code transcripts,
subagents included, that names `magus refs`, `explain`, `path`, `query`, `grep` or
`rg`: 198,579 calls, deduplicated by tool call id, through 2026-11-06. Each count
below is a regular-expression match on the command text, so it shows how often an
answer was looked for, not whether the call succeeded.

`refs` use by mode, of 17,680 calls:

| mode                          | calls |
| ----------------------------- | ----: |
| `--source`                    | 6,304 |
| `--occurrences`               | 5,128 |
| `--definition`                | 5,022 |
| plain list of referring files | 4,366 |
| `--text`                      | 2,054 |
| `--rename`                    |   303 |

`--definition` and `--source` are mostly typed together, so those rows overlap.
A subcommand word typed as the first argument appears 14 times: `show` 6, `rename` 4,
`symbols` 2, `tree` 1 and `list` 1.

The graph already holds the edges a symbol question needs
(`types/knowledge.go:172-206`):

- `implements` links an implementing symbol to its interface, from the SCIP index
  (`internal/graph/knowledge/symbols.go:216`).
- `defines` links a file to each symbol it defines.
- `calls` links a Go file to each symbol it calls. Function-to-function calls are
  recorded for Buzz only (`internal/graph/knowledge/buzz.go:190`).

`magus explain <node>` prints a node's edges in both directions with their labels
("implemented by", "defines"), and it takes `symbol:` and `file:` ids. At
`be17ecc08`, `explain` on the `vcs` interface `CheckoutIDReader#` prints
`implemented by (10)`, `gitVCS#`, `hgVCS#`, `jjVCS#` and `saplingVCS#` among them.

## Decision

`refs` gets no new subcommand and no new flag. Each candidate is listed below with
its use and what already answers it.

| question                         | uses in the trail                                                | already answered by                           | State     |
| -------------------------------- | ---------------------------------------------------------------- | --------------------------------------------- | --------- |
| a symbol's body (`show`)         | 6,304 `--source`                                                 | `refs <symbol> --source`                      | done      |
| the symbols a file defines       | 1,943 `grep -n '^func \|^type' <file>`, 466 `explain file:`      | `explain file:<path>`, and the guard sends it | done      |
| who implements an interface      | 72 greps naming "implement", 8 for a method receiver, 0 in query | `explain symbol:<interface>`                  | done      |
| who calls a function             | 43 greps for `Name(`, 3 `query relation=calls`                   | `refs <symbol>`, then `--occurrences`         | done      |
| what a function calls            | none found                                                       | `explain symbol:` for Buzz; nothing for Go    | not built |
| which symbols nothing references | none found                                                       | nothing                                       | not built |
| `refs show`, `refs ls` and kin   | 14 first-word guesses                                            | the flags; refused 2026-10-31                 | not built |

The outline is the only candidate with heavy use, and it already has a home. The
guard turns a one-file search whose every hit declares an indexed symbol into
`magus explain file:<path>` (`internal/guard/catalog.go:605`). The tests at
`internal/guard/translate_test.go:306-326` pin it. The denial itself prints each
`line: name` pair (`internal/guard/translate.go:984`), so the agent gets the
outline from the refusal without a second call. `explain file:` then lists the
same names as full symbol ids, without line numbers: at `be17ecc08`,
`explain file:internal/cache/lock.go` prints `defines (14)` and no lines.

### Principles checked

- [Delete before you add](../design/principles/delete-before-you-add.md): relied
  on. Every answered question stays with the verb that answers it now.
- [Unix survives at the verb](../design/principles/unix-survives-at-the-verb.md):
  relied on. `refs` answers where a symbol is and who names it. `explain` answers
  what one node is connected to. An outline or an implementer list is the second
  question, so it stays with `explain`.
- [Measured, not asserted](../design/principles/measured-not-asserted.md): relied
  on. Each row names its count. A candidate with no use was not built on a guess.

## Alternatives

### `refs <file>` or `refs --outline` for the outline

It lost because the guard's denial already prints the names and their lines, and
`explain file:<path>` lists them. A second spelling would be a second door to one
answer. The missing line numbers in `explain file:` are a defect of that card, not
a reason for a new mode on `refs`. The 1,943 outline greps are its evidence. The translation landed on
2026-09-27 (#438), and the trail as extracted carries no timestamps, so how many
greps came after it is not measured. `./magus session hints` has no row for the
translation in this checkout's loaded sessions. It would reopen if that translation
were served and then refused in a measurable share of calls.

### `refs --implementers` or `refs --callers`

It lost because `explain symbol:<id>` lists implementers, and the referring-file
list from `refs` is already the caller list at file level. Function-level callers
for Go would need a `calls` edge between symbols, which the Go index does not
record. Eighty-one greps across the whole trail do not justify that ingest. The
evidence is the counts in the table above. It would reopen if `refs` output were
followed by a hand search for the enclosing function in a measurable share of calls.

### `refs --unused` for symbols nothing references

It lost because no call in the trail asked for it. A dead-code check also belongs
in a lint, where `golangci-lint`'s `unused` already runs for Go (`.golangci.yml:19`). It would reopen if
a language with no such linter needed one and agents searched for it.

### The subcommand split

It lost on 2026-10-31. The study behind that refusal measured about 390 changed
lines and a reserved word per mode, against a saving of about 3 tokens a call. Fourteen guesses in 17,680
calls do not change that.

## Consequences

`refs` keeps one verb with flag modes, and `magus -h` keeps 36 commands. People and
models reach implementers and outlines through `explain`, mostly because the guard
hands them that command. Nothing is built.

## Open questions

- Should `explain` resolve a bare symbol name, as `refs` does? Today
  `explain JoinInflight` searches domain entities only and points at `refs`, so
  each of the 706 `explain symbol:` calls in the trail named the full id.
- Should `explain file:` print each defined symbol's line and short name? The
  guard's denial already does; the card is the one place an outline is missing
  them.
- Go has no function-to-function `calls` edge. If one is ever ingested, "what a
  function calls" becomes an `explain` answer with no new flag.
