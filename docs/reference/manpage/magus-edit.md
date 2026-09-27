---
title: magus edit
generated_from: internal/cli/registry.go
description: "Apply an edit set declared as JSON: every site is checked against the file on disk before anything is written, every file is written or none is, and a receipt records the undo."
tags: [cli, magus edit, edit, multi-file, atomic, undo, receipt]
---

# magus-edit

Apply a multi-file edit set: every site checked first, all files written or none

## Synopsis

**magus** edit --stdin [--check] | --undo \<id\> [--check] | --schema

## Description

Apply an edit set, a JSON document naming sites in one or more files and
the bytes that replace each. The caller writes every replacement; magus
checks that each site holds the bytes the caller expects, then writes.

A site is anchored by "lines", whole lines by number ([start, end], 1-based
and inclusive; end = start-1 inserts before line start), or by "text", the
bytes equal to old (exactly one occurrence, or every one with "all"). "old"
is required for text and checked when given for lines. "digest", the
sha256:\<hex\> of the whole file as the caller read it, refuses a file that
changed since.

Every site is resolved before the first byte moves. One bad site refuses the
whole set, every reason is listed, and nothing is written. A declared output
is refused: regenerate it, never hand-edit. There is no --force.

The write stages each file as a temp sibling, renames the stages over the
originals, and renames the held originals back if any rename fails. No VCS
command runs. The receipt, stored under the cache dir, holds the undo set:
--undo \<id\> applies it, refusing any file edited since.

-o name prints the receipt id; -o json the receipt.

## Options

**--check**
: Resolve and check every site and print the plan; write nothing

**--schema**
: Print the JSON schema an edit set must satisfy, and exit

**--stdin**
: Read the edit set as JSON on stdin

**--undo** *string*
: Apply the undo set of the receipt with this id

## Exit status

**0**
: The set was applied, or with --check would apply.

**1**
: The set was refused and nothing was written, or a write failed and every file already written was restored. Each reason is printed.

**2**
: Misuse, or the input is not a readable edit set; nothing was attempted.

## Examples

*Apply a set*

```sh
magus edit --stdin < edits.json
```

*Check a set without writing*

```sh
magus edit --check --stdin < edits.json
```

*Undo it*

```sh
magus edit --undo 20260926-101500-1a2b3c4d
```

*Print the set schema*

```sh
magus edit --schema
```

## See Also

[**magus**(1)](magus.md), [**magus-ls**(1)](magus-ls.md), [**magus-describe**(1)](magus-describe.md), [**magus-run**(1)](magus-run.md), [**magus-x**(1)](magus-x.md), [**magus-where**(1)](magus-where.md), [**magus-affected**(1)](magus-affected.md), [**magus-graph**(1)](magus-graph.md), [**magus-query**(1)](magus-query.md), [**magus-explain**(1)](magus-explain.md), [**magus-path**(1)](magus-path.md), [**magus-refs**(1)](magus-refs.md), [**magus-watch**(1)](magus-watch.md), [**magus-events**(1)](magus-events.md), [**magus-status**(1)](magus-status.md), [**magus-clean**(1)](magus-clean.md), [**magus-shell**(1)](magus-shell.md), [**magus-vcs**(1)](magus-vcs.md), [**magus-queue**(1)](magus-queue.md), [**magus-doctor**(1)](magus-doctor.md), [**magus-config**(1)](magus-config.md), [**magus-session**(1)](magus-session.md), [**magus-memory**(1)](magus-memory.md), [**magus-job**(1)](magus-job.md), [**magus-notes**(1)](magus-notes.md), [**magus-diff**(1)](magus-diff.md), [**magus-server**(1)](magus-server.md), [**magus-broker**(1)](magus-broker.md), [**magus-mcp**(1)](magus-mcp.md), [**magus-buzz**(1)](magus-buzz.md), [**magus-completion**(1)](magus-completion.md), [**magus-man**(1)](magus-man.md), [**magus-init**(1)](magus-init.md), [**magus-spell**(1)](magus-spell.md), [**magus-agent**(1)](magus-agent.md), [**magus-self**(1)](magus-self.md), [**magus-version**(1)](magus-version.md)

