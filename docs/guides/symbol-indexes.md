---
title: Symbol indexes
description: Which languages magus indexes itself, which need an external SCIP indexer, and how a workspace opts one in with spells.symbol_indexers in magus.yaml.
tags:
  [
    symbols,
    symbol index,
    scip,
    scip-typescript,
    scip-python,
    rust-analyzer,
    typescript,
    javascript,
    python,
    rust,
    refs,
    graph build,
  ]
---

# Symbol indexes

`magus refs`, `magus explain` on a symbol, and `magus query "kind=symbol ..."` answer
from a symbol index: every definition, reference and call in a project. A project gets
one from the spell it binds, which declares its indexer with `mgs_getSymbolIndexer`.
`magus graph build` runs those indexers and stores what they write in magus's own
format.

<!--diagram:symbol-index-->

## In-process indexers

magus indexes Go itself, in process, re-checking only the packages an edit moved. A
project bound to the go spell has a symbol index with nothing to install and nothing
to configure.

## External indexers

TypeScript and JavaScript, Python and Rust are indexed by open-source tools that emit
SCIP, which magus imports into its own store:

| Spell      | Indexer           | Covers                                       |
| ---------- | ----------------- | -------------------------------------------- |
| typescript | `scip-typescript` | `.ts`, `.tsx`, `.mts`, `.cts`, `.js`, `.jsx` |
| python     | `scip-python`     | `.py`                                        |
| rust       | `rust-analyzer`   | `.rs`                                        |

An external indexer never runs by default. It forks a tool the workspace installs, and
it is slower than an in-process one: it re-indexes the whole project on every change,
where the Go indexer re-checks only what moved. Until a workspace opts one in, its
language has no symbol index, and a lookup names the project as outside coverage:

```text
verdict: unknown, not absent
  outside coverage: web (no symbol index for typescript: scip-typescript is an external SCIP indexer and runs only when opted in; add typescript to spells.symbol_indexers in magus.yaml)
```

## Opt one in

List the spell under `spells.symbol_indexers` in `magus.yaml`:

```yaml
spells:
  symbol_indexers: [typescript]
```

Then install the indexer where a run finds it. Pin it with the workspace's other tools,
for example in `mise.toml`:

```toml
[tools]
"npm:@sourcegraph/scip-typescript" = "0.4.0"
```

Run `magus graph build`. Each project bound to the typescript spell now has a symbol
index, and `magus refs <symbol>` answers there. The indexer's version keys the index, so
upgrading the tool rebuilds it.

A spell listed in `symbol_indexers` whose indexer is not installed fails its index run
and names the missing binary. A name listed twice is a configuration error.
