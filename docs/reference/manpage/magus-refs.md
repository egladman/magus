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

--text switches to that other question on purpose: a literal substring
search with no symbol index and no graph, printed as path:line:text like
every other grep-shaped tool. It is the replacement a guard deny routes a
recursive grep to, so it answers on a cold worktree with no index built.
Its exit code is grep's (0 matched, 1 no match, 2 error), not the verdict
codes the symbol lookup above uses - the two modes answer different
questions and are not meant to share a contract.

## Options

**--no-generated** *string*
: In the fallback text search shown beside a symbol miss, or with --text, exclude declared-output files entirely instead of searching them and marking the ones that match

**--occurrences**
: Every exact source range, uncapped and verified against the tree - the view a mechanical edit needs, where the default line list is capped and describes fan-in

**--refresh**
: Re-ingest the SCIP index before answering

**--text**
: Raw substring search, no symbol index: print path:line:text matches and exit 0/1/2 for matched/no-match/error (grep's contract, not refs' verdict exit codes). Trailing paths scope the search, as grep's do; without any it searches the workspace

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

*Raw text search, no index needed*

```sh
magus refs TODO --text
```

## See Also

[**magus**(1)](magus.md), [**magus-ls**(1)](magus-ls.md), [**magus-describe**(1)](magus-describe.md), [**magus-run**(1)](magus-run.md), [**magus-x**(1)](magus-x.md), [**magus-where**(1)](magus-where.md), [**magus-affected**(1)](magus-affected.md), [**magus-graph**(1)](magus-graph.md), [**magus-query**(1)](magus-query.md), [**magus-explain**(1)](magus-explain.md), [**magus-path**(1)](magus-path.md), [**magus-watch**(1)](magus-watch.md), [**magus-events**(1)](magus-events.md), [**magus-status**(1)](magus-status.md), [**magus-clean**(1)](magus-clean.md), [**magus-shell**(1)](magus-shell.md), [**magus-vcs**(1)](magus-vcs.md), [**magus-doctor**(1)](magus-doctor.md), [**magus-config**(1)](magus-config.md), [**magus-session**(1)](magus-session.md), [**magus-memory**(1)](magus-memory.md), [**magus-job**(1)](magus-job.md), [**magus-notes**(1)](magus-notes.md), [**magus-diff**(1)](magus-diff.md), [**magus-server**(1)](magus-server.md), [**magus-mcp**(1)](magus-mcp.md), [**magus-buzz**(1)](magus-buzz.md), [**magus-completion**(1)](magus-completion.md), [**magus-man**(1)](magus-man.md), [**magus-init**(1)](magus-init.md), [**magus-agent**(1)](magus-agent.md), [**magus-self**(1)](magus-self.md), [**magus-version**(1)](magus-version.md)

