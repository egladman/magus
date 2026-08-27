---
title: magus describe
generated_from: internal/cli/registry.go
description: Define a magus concept (spell, charm, target, project, workspace, module, mcp-tool) and list every entity of that kind, or detail one when a name is given.
tags: [cli, magus describe, spell, charm, target, project, workspace, introspection]
---

# magus-describe

Define a magus concept and list its entities

## Synopsis

**magus** describe \<noun\> [\<name\>] [flags]

## Description

Define a magus concept and list every entity of that kind. The noun is
one of spell, charm, target, project, workspace, module, or mcp-tool; singular
and plural are interchangeable. Pass a name after the noun to detail a single
entity instead of listing them all. (The knowledge graph lives under magus
graph: export for the merged graph, stats for its shape.)

The charm noun is the inverse of a target ref: "describe charm rw" lists every
target that declares the rw charm and the argv edit each one makes, the transpose
of the charms a single "describe target" lists.

For a target ref (e.g. "api:build", or ":test" for all projects) magus prints the
fully-evaluated dispatch plan: the workspace-rooted source and output globs, the
spells that fire, the charm-applied command, and any per-target policy. A target
that composes others also prints a "chain" line naming them in invocation order
(e.g. "generate -\> lint -\> build -\> test"), read from the ctx.needs calls in the
magusfile; a cross-project step reads as "project:target". It lists DIRECT steps
only, so a step that itself composes is described by its own ref. Add a charm
and --explain (e.g. "lint:rw --explain") to see each charm reshape the command one
step at a time.

### describe target options

**--against** *ref*
: With --cache: diff the live key inputs against the stored lines behind an output \`ref\`

**--cache**
: Show the live cache key, the ref a run would print, the component classes behind it, and what moved since the last recorded run

**-e**
: Short for --explain

**--explain**
: For a ref with charms: show the per-charm argv trace (base then each charm)

**--inputs**
: With --cache: list every key input line, so you can confirm a declared file was actually hashed

**--no-default-charms**
: With --cache: ignore magus.yaml default_charms when keying, matching a run made the same way (CI)

### describe projects options

**-e**
: Short for --evaluated

**--evaluated**
: Print workspace-rooted globs, effective claims, and per-target policies

### describe spells options

**--versions**
: Probe each spell's tools and report the versions that would key a run

## Subcommands

**targets**
: List every target the workspace defines

**target**
: Detail one target ref: its dispatch plan, globs, spells and policy

**projects**
: List the workspace's projects

**spells**
: List the spells the workspace resolves

**charms**
: List charms and the targets that declare them

**workspaces**
: List the workspaces registered in config

**modules**
: List the Buzz host modules a magusfile can import

**mcp-tools**
: List the tools the MCP server exposes

**tools**
: List the external tools the workspace's spells require

**file**
: Classify paths as generated output, declared source, maintained, or unclaimed

**graph**
: Emit the target catalog and dependency graph

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

[**magus**(1)](magus.md), [**magus-ls**(1)](magus-ls.md), [**magus-run**(1)](magus-run.md), [**magus-x**(1)](magus-x.md), [**magus-where**(1)](magus-where.md), [**magus-affected**(1)](magus-affected.md), [**magus-graph**(1)](magus-graph.md), [**magus-query**(1)](magus-query.md), [**magus-explain**(1)](magus-explain.md), [**magus-path**(1)](magus-path.md), [**magus-refs**(1)](magus-refs.md), [**magus-watch**(1)](magus-watch.md), [**magus-status**(1)](magus-status.md), [**magus-clean**(1)](magus-clean.md), [**magus-vcs**(1)](magus-vcs.md), [**magus-doctor**(1)](magus-doctor.md), [**magus-config**(1)](magus-config.md), [**magus-session**(1)](magus-session.md), [**magus-memory**(1)](magus-memory.md), [**magus-notes**(1)](magus-notes.md), [**magus-diff**(1)](magus-diff.md), [**magus-server**(1)](magus-server.md), [**magus-buzz**(1)](magus-buzz.md), [**magus-completion**(1)](magus-completion.md), [**magus-man**(1)](magus-man.md), [**magus-init**(1)](magus-init.md), [**magus-agent**(1)](magus-agent.md), [**magus-self**(1)](magus-self.md), [**magus-version**(1)](magus-version.md)

