---
title: magus notes
generated_from: internal/cli/registry.go
description: "Read the workspace's human-authored notes: prose a person wrote about the code, anchored to graph entities but derived from none of them."
tags: [cli, magus notes, notes, knowledge, annotations]
---

# magus-notes

Human-authored notes committed to the repository

## Synopsis

**magus** notes \<ls|get|edit|verify\> [flags]

## Description

Read the workspace's human-authored notes.

A note carries knowledge whose only provenance is a person: something no
extractor could derive from the tree, and that no rebuild would recover. That
is what separates it from the two things it is NOT. Documentation describes the
system for a reader and lives in the docs tree. A NOTE/WHY/TODO comment marks
one location in one file. A note attaches to graph ENTITIES - a symbol, a file,
a project, a target, another note - and may span several at once, which is how
it can record something like "these two caches must be invalidated together"
that no single comment could hold.

Anchors are required, and they name node IDs rather than positions. A symbol
anchor survives the code moving file and line and breaks only on a rename or a
deletion, which is exactly when the note should be re-read; a line number would
break on the next edit above it with nothing to detect.

There is no put. Notes are written by a person in their own editor and
committed under their own name, which is what makes git attribution meaningful
and what keeps the store worth trusting. Set knowledge.notes.path in magus.yaml
to declare where they live; with nothing declared the feature is inert.

## Subcommands

**ls**
: Show notes and any repair warnings

**get**
: Show one note

**edit**
: Open one note in $VISUAL or $EDITOR

**verify**
: Check malformed notes and anchors that no longer resolve

## Examples

*List every note*

```sh
magus notes ls
```

*Read one note*

```sh
magus notes get cache-invalidation-pairing
```

*Write or revise one*

```sh
magus notes edit cache-invalidation-pairing
```

*Check every anchor still resolves*

```sh
magus notes verify
```

## See Also

[**magus**(1)](magus.md), [**magus-ls**(1)](magus-ls.md), [**magus-describe**(1)](magus-describe.md), [**magus-run**(1)](magus-run.md), [**magus-x**(1)](magus-x.md), [**magus-where**(1)](magus-where.md), [**magus-affected**(1)](magus-affected.md), [**magus-graph**(1)](magus-graph.md), [**magus-query**(1)](magus-query.md), [**magus-explain**(1)](magus-explain.md), [**magus-path**(1)](magus-path.md), [**magus-refs**(1)](magus-refs.md), [**magus-watch**(1)](magus-watch.md), [**magus-events**(1)](magus-events.md), [**magus-status**(1)](magus-status.md), [**magus-clean**(1)](magus-clean.md), [**magus-vcs**(1)](magus-vcs.md), [**magus-doctor**(1)](magus-doctor.md), [**magus-config**(1)](magus-config.md), [**magus-session**(1)](magus-session.md), [**magus-memory**(1)](magus-memory.md), [**magus-diff**(1)](magus-diff.md), [**magus-server**(1)](magus-server.md), [**magus-buzz**(1)](magus-buzz.md), [**magus-completion**(1)](magus-completion.md), [**magus-man**(1)](magus-man.md), [**magus-init**(1)](magus-init.md), [**magus-agent**(1)](magus-agent.md), [**magus-self**(1)](magus-self.md), [**magus-version**(1)](magus-version.md)

