---
title: magus describe
description: Define a magus concept (spell, charm, target, project, workspace, module, mcp-tool, knowledge) and list every entity of that kind, or detail one when a name is given.
tags: [cli, magus describe, spell, charm, target, project, workspace, introspection]
---

# magus-describe

Define a magus concept and list its entities

## Synopsis

**magus** describe \<noun\> [\<name\>] [flags]

## Description

Define a magus concept and list every entity of that kind. The noun is
one of spell, charm, target, project, workspace, module, mcp-tool, or knowledge;
singular and plural are interchangeable. Pass a name after the noun to detail a
single entity instead of listing them all.

The knowledge noun emits the deterministic knowledge graph of the workspace
domain (projects, targets, spells, ops, charms, modules, methods, diagnostics)
as a merged node-link graph; use -o json to feed it to an external graph tool.

The charm noun is the inverse of a target ref: "describe charm rw" lists every
target that declares the rw charm and the argv edit each one makes, the transpose
of the charms a single "describe target" lists.

For a target ref (e.g. "api:build", or ":test" for all projects) magus prints the
fully-evaluated dispatch plan: the workspace-rooted source and output globs, the
spells that fire, the charm-applied command, and any per-target policy. Add a charm
and --explain (e.g. "lint:rw --explain") to see each charm reshape the command one
step at a time.

## Options

**--evaluated**
: For projects: print workspace-rooted globs, effective claims, and per-target policies

**--explain**
: For a target ref with charms: show the per-charm argv trace (base then each charm)

## Examples

*List every target*

```sh
magus describe targets
```

*List a charm's declaring targets*

```sh
magus describe charm rw
```

*Detail one project*

```sh
magus describe project api
```

*Preview a charm-applied command*

```sh
magus describe target lint:rw
```

*Trace how each charm reshapes the command*

```sh
magus describe target --explain lint:rw,debug
```

## See Also

[**magus**(1)](magus.md), [**magus-ls**(1)](magus-ls.md), [**magus-run**(1)](magus-run.md), [**magus-x**(1)](magus-x.md), [**magus-where**(1)](magus-where.md), [**magus-tail**(1)](magus-tail.md), [**magus-affected**(1)](magus-affected.md), [**magus-insight**(1)](magus-insight.md), [**magus-watch**(1)](magus-watch.md), [**magus-status**(1)](magus-status.md), [**magus-doctor**(1)](magus-doctor.md), [**magus-config**(1)](magus-config.md), [**magus-server**(1)](magus-server.md), [**magus-completion**(1)](magus-completion.md), [**magus-init**(1)](magus-init.md), [**magus-self**(1)](magus-self.md), [**magus-version**(1)](magus-version.md)

