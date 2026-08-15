---
title: magus diff
description: "Report every uncommitted change annotated with what the workspace knows: whether it is generated, how widely its changed symbols are referenced, whether it is public API surface, and what coverage was observed."
tags: [cli, magus diff, diff, review, changeset, semver]
---

# magus-diff

Read the working tree's changes in the order they deserve attention

## Synopsis

**magus** diff [--generated] [--tui] [--watch] [\<patch-file\>|-] [flags]

## Description

Read the working tree's uncommitted changes, annotated and ordered.

A changeset is not a list of files, it is a set of CONSEQUENCES, and a reader's
attention is scarce. Alphabetical order spends it at random: it gives a
regenerated lockfile the same weight as a signature change twelve packages
depend on. This orders by what a change can BREAK.

Generated files - declared target outputs - are folded away by default. Reading
one is reading a machine's restatement of a change made somewhere else, so the
source edit is the review. Pass --generated to see them anyway.

Each file carries the evidence behind its rank: how many files reference the
widest changed symbol it defines, whether any of those referents cross a project
boundary or the module boundary (which is the question a version bump turns on),
and the coverage a prior run observed. None of it is a verdict. magus does not
claim a change is breaking - deciding that needs signature compatibility, which
needs a base-side index magus does not keep and language semantics it does not
model - it reports who can see the thing you changed and lets you decide.

The console's Diff surface reads the same annotations over the same session,
and an agent can join that session through the magus_diff MCP tool.

## Options

**--generated**
: Include declared target outputs, which are folded away by default

**--tui**
: Read the changeset interactively, joined to the session the console and an agent share

**--watch**
: Re-read and re-render whenever the working tree changes

## Examples

*Read what you are about to commit*

```sh
magus diff
```

*Include the generated files too*

```sh
magus diff --generated
```

*Navigate it hunk by hunk and mark what you have read*

```sh
magus diff --tui
```

*Machine-readable, for a script or a Buzz advisor*

```sh
magus diff -o json
```

## See Also

[**magus**(1)](magus.md), [**magus-ls**(1)](magus-ls.md), [**magus-describe**(1)](magus-describe.md), [**magus-run**(1)](magus-run.md), [**magus-x**(1)](magus-x.md), [**magus-where**(1)](magus-where.md), [**magus-affected**(1)](magus-affected.md), [**magus-graph**(1)](magus-graph.md), [**magus-query**(1)](magus-query.md), [**magus-explain**(1)](magus-explain.md), [**magus-path**(1)](magus-path.md), [**magus-refs**(1)](magus-refs.md), [**magus-watch**(1)](magus-watch.md), [**magus-status**(1)](magus-status.md), [**magus-clean**(1)](magus-clean.md), [**magus-vcs**(1)](magus-vcs.md), [**magus-doctor**(1)](magus-doctor.md), [**magus-config**(1)](magus-config.md), [**magus-memory**(1)](magus-memory.md), [**magus-notes**(1)](magus-notes.md), [**magus-server**(1)](magus-server.md), [**magus-buzz**(1)](magus-buzz.md), [**magus-completion**(1)](magus-completion.md), [**magus-man**(1)](magus-man.md), [**magus-init**(1)](magus-init.md), [**magus-agent**(1)](magus-agent.md), [**magus-hook**(1)](magus-hook.md), [**magus-notify**(1)](magus-notify.md), [**magus-self**(1)](magus-self.md), [**magus-version**(1)](magus-version.md)

