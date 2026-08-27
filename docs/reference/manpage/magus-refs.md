---
title: magus refs
generated_from: internal/cli/registry.go
description: "List a symbol's definition and every file that references it as file:line rows, read from the declared SCIP index rather than found by text search."
tags: [cli, magus refs, symbols, scip, references, knowledge graph]
---

# magus-refs

List where an ingested code symbol is defined and referenced

## Synopsis

**magus** refs \<symbol\> [flags]

## Description

List where an ingested code symbol is defined and every file that
references it, as file:line rows drawn from the SCIP index.

This is the occurrence-shaped view a symbol's fan-in needs: a flat list,
which is what you want when the question is "who calls this". The
node-link neighborhood that magus query renders is the wrong shape for
that question, which is why this is its own command rather than a flag.

The argument is a symbol node ID (symbol:...) or a name that resolves to
one. Symbols come from a declared SCIP index; see knowledge.symbols in
the configuration. A workspace with no index has no symbols to report,
and says so rather than falling back to a text search - a grep result
and an index result answer different questions, and quietly substituting
one for the other is how a wrong answer looks right.

## Options

**--occurrences**
: Every exact source range, uncapped and verified against the tree - the view a mechanical edit needs, where the default line list is capped and describes fan-in

**--refresh**
: Re-ingest the SCIP index before answering

## Examples

*Every reference to a symbol*

```sh
magus refs Open
```

*By fully-qualified node ID*

```sh
magus refs symbol:github.com/egladman/magus/Open
```

*As JSON*

```sh
magus refs Open -o json
```

## See Also

[**magus**(1)](magus.md), [**magus-ls**(1)](magus-ls.md), [**magus-describe**(1)](magus-describe.md), [**magus-run**(1)](magus-run.md), [**magus-x**(1)](magus-x.md), [**magus-where**(1)](magus-where.md), [**magus-affected**(1)](magus-affected.md), [**magus-graph**(1)](magus-graph.md), [**magus-query**(1)](magus-query.md), [**magus-explain**(1)](magus-explain.md), [**magus-path**(1)](magus-path.md), [**magus-watch**(1)](magus-watch.md), [**magus-status**(1)](magus-status.md), [**magus-clean**(1)](magus-clean.md), [**magus-vcs**(1)](magus-vcs.md), [**magus-doctor**(1)](magus-doctor.md), [**magus-config**(1)](magus-config.md), [**magus-session**(1)](magus-session.md), [**magus-memory**(1)](magus-memory.md), [**magus-notes**(1)](magus-notes.md), [**magus-diff**(1)](magus-diff.md), [**magus-server**(1)](magus-server.md), [**magus-buzz**(1)](magus-buzz.md), [**magus-completion**(1)](magus-completion.md), [**magus-man**(1)](magus-man.md), [**magus-init**(1)](magus-init.md), [**magus-agent**(1)](magus-agent.md), [**magus-self**(1)](magus-self.md), [**magus-version**(1)](magus-version.md)

