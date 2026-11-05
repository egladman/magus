---
title: magus refs
generated_from: internal/commands/registry.go
description: "List a symbol's definition and every file that references it as file:line rows, read from the declared SCIP index rather than found by text search."
tags: [cli, magus refs, symbols, scip, references, knowledge graph]
---

# magus-refs

List where an ingested code symbol is defined and referenced

## Synopsis

**magus** refs \<symbol\> [\<path\>...] [flags]

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

--definition answers "where is the body" with the exact lines: each
definition as path:start-end, from the index's enclosing range, checked
against the file on disk: verified when the file predates its index,
changed (exit 1) when the symbol's name has left the start line, and
unverified when the name is there but the file was edited since. An index
that recorded no end line says so instead of guessing one. --source adds
the lines themselves; with --occurrences it prints each site's lines
instead. --context N adds N lines on both sides, and --before and --after
set one side over it. --lines A:B picks a range counted from the match: 0
is the matched line or a definition's whole body, -1 the line above it, 1
the line below it, so --lines=-5:-1 prints only the 5 lines above. Any of
these implies --source, all clamp to the file, and a range with no lines
says so. Blocks number their lines and mark the matched one with \>.
--max-lines caps each block at 200 lines by default (0 prints all) and
--skip-lines pages through it; a cut block prints the lines shown, the total
and the next command.

A trailing path keeps the plain list or the --occurrences sites at or under it,
as it scopes --text, and a path that does not exist is an error. --limit and
--offset page the sites; a cut page prints the total and the next command.

--text switches to that other question on purpose: a literal substring
search with no symbol index and no graph, printed as path:line:text like
every other grep-shaped tool. It is the replacement a guard deny routes a
recursive grep to, so it answers on a cold worktree with no index built.
Its exit code is grep's (0 matched, 1 no match, 2 error), not the verdict
codes the symbol lookup above uses - the two modes answer different
questions and are not meant to share a contract.

--rename \<new\> is the one write: it replaces the symbol's name at every
site --occurrences verified, and nowhere else. A bare name must resolve
to exactly one symbol defined in the workspace; pass the symbol ID
otherwise. Before anything is written, every file is checked and graded
under the acting lease by the rules a host edit meets: a stale or
missing index, an unverified site, a declared output, or a guard deny
refuses the whole rename. Then every file is written or none is. The
global --dry-run resolves and grades every site and prints them, writing
nothing. The inverse is the same command with the two names swapped.

## Options

**--after** *int*
: Print this many lines below each range, over --context, clamped to the file (grep -A). Implies --source

**--before** *int*
: Print this many lines above each range, over --context, clamped to the file (grep -B). Implies --source

**--context** *int*
: Print this many lines on both sides of each range, clamped to the file (grep -C). Implies --source

**--[no-]definition** (default: off)
: Print each definition as path:start-end, the lines its body spans, checked against the file on disk. A range the index did not record is said, never guessed

**--[no-]generated** (default: on)
: In the fallback text search shown beside a symbol miss, or with --text, search declared-output files and mark the ones that match; --no-generated excludes them entirely

**--limit** *int*
: Print at most this many --text matches or --occurrences sites, then say how many there were (0 for all). What piping to head would do, without losing the count or the exit code

**--lines** *string*
: Print the range A:B counted from each match: 0 is the match however long, -1 the line above it, 1 the line below, as --lines=-5:-1. Implies --source; takes none of --context, --before, --after

**--max-lines** *int* (default: 200)
: Print at most this many lines of each block (0 for all), then say how many there were and print the next command. Implies --source

**--[no-]occurrences** (default: off)
: Every exact source range, uncapped and verified against the tree: the view a mechanical edit needs. The default line list is capped and describes fan-in.

**--offset** *int*
: With --occurrences, skip this many sites first; a cut page prints the next command

**--[no-]refresh** (default: off)
: Re-ingest the SCIP index before answering

**--rename** *string*
: Rename the symbol to this name at every verified occurrence, after grading every file under the acting lease; every file is written or none is

**--skip-lines** *int*
: Skip this many lines of each block first; line numbers carry on. Implies --source

**--[no-]source** (default: off)
: With --definition (implied), also print the definition's lines: a symbol's body by name, in place of grep -n then sed -n. With --occurrences, print each site's lines

**--[no-]text** (default: off)
: Raw substring search, no symbol index: print path:line:text matches and exit 0/1/2 for matched/no-match/error (grep's contract, not refs' verdict exit codes). Trailing paths scope it, as grep's do; with none it searches the workspace

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

*Where a symbol's body starts and ends*

```sh
magus refs Open --definition
```

*A symbol's body, by name*

```sh
magus refs Open --source
```

*Every site's code, with five lines on both sides*

```sh
magus refs Open --occurrences --context 5
```

*Only the five lines above each site*

```sh
magus refs Open --occurrences --lines=-5:-1
```

*Five lines above each site, four below*

```sh
magus refs Open --occurrences --before 5 --after 4
```

*One page of sites*

```sh
magus refs Open --occurrences --limit 5 --offset 5
```

*The sites under one path*

```sh
magus refs Open internal/guard --occurrences --context 2
```

*Raw text search, no index needed*

```sh
magus refs TODO --text
```

*Preview a rename*

```sh
magus refs parseQuery --rename parseTerms --dry-run
```

*Rename a symbol at every verified site*

```sh
magus refs parseQuery --rename parseTerms
```

## See Also

[**magus**(1)](magus.md), [**magus-ls**(1)](magus-ls.md), [**magus-describe**(1)](magus-describe.md), [**magus-where**(1)](magus-where.md), [**magus-run**(1)](magus-run.md), [**magus-affected**(1)](magus-affected.md), [**magus-x**(1)](magus-x.md), [**magus-clean**(1)](magus-clean.md), [**magus-shell**(1)](magus-shell.md), [**magus-query**(1)](magus-query.md), [**magus-explain**(1)](magus-explain.md), [**magus-graph**(1)](magus-graph.md), [**magus-diff**(1)](magus-diff.md), [**magus-vcs**(1)](magus-vcs.md), [**magus-session**(1)](magus-session.md), [**magus-job**(1)](magus-job.md), [**magus-notes**(1)](magus-notes.md), [**magus-queue**(1)](magus-queue.md), [**magus-watch**(1)](magus-watch.md), [**magus-events**(1)](magus-events.md), [**magus-server**(1)](magus-server.md), [**magus-broker**(1)](magus-broker.md), [**magus-mcp**(1)](magus-mcp.md), [**magus-status**(1)](magus-status.md), [**magus-buzz**(1)](magus-buzz.md), [**magus-spell**(1)](magus-spell.md), [**magus-agent**(1)](magus-agent.md), [**magus-init**(1)](magus-init.md), [**magus-doctor**(1)](magus-doctor.md), [**magus-config**(1)](magus-config.md), [**magus-self**(1)](magus-self.md), [**magus-version**(1)](magus-version.md)

