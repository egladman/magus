---
title: magus explain
generated_from: internal/commands/registry.go
description: "Show a single knowledge-graph node in context: its data, its incoming and outgoing edges with provenance, and how many nodes reach it."
tags: [cli, magus explain, explain, knowledge graph, node, edges, provenance, impact, shortest path, relationship, coupling]
---

# magus-explain

Show one knowledge-graph node's context: data, edges, and reach

## Synopsis

**magus** explain \<node-id-or-name\> [--to \<node-id-or-name\>] [flags]

## Description

explain shows one node's context: its data, its incoming and outgoing
edges with provenance, and how many nodes reach it. Where query finds candidates,
explain is what you run on the one you picked.

The argument is a node ID (target:pkg/foo:build) or a name that resolves to one
(build). Names are convenient and IDs are unambiguous; when a name matches more than
one node, resolve it with query first and pass the ID.

Two parts of the output are worth knowing about. Every edge carries its PROVENANCE -
what declared it, so a surprising relationship can be traced to the file that created
it rather than taken on faith. And the reach count answers the blast-radius question
directly: how many nodes can arrive at this one.

With --to, explain answers how the node is related to a second one: the shortest
chain of edges between them, each hop's relation named. Ask it when a change to one
thing breaks a seemingly unrelated other, or when two projects look coupled and you
want the chain rather than a hunch. Edges are walked in EITHER direction, so a chain
proves the two nodes are connected, not that influence flows from one to the other;
read each hop's relation to see which way it points.

The graph is cache-backed under \<cache\>/knowledge and only shards whose sources
changed are rebuilt; --refresh forces a full rebuild.

## Options

**--[no-]global** (default: off)
: Resolve across the workspaces registered in config (knowledge.workspaces)

**--max-lines** *int* (default: 200)
: Print at most this many lines of the section (0 for all), then say how many there were and print the next command. Implies --source

**--[no-]refresh** (default: off)
: Force a full graph rebuild before explaining

**--skip-lines** *int*
: Skip this many lines of the section first; line numbers carry on. Implies --source

**--[no-]source** (default: off)
: Print a doc section's lines from its indexed extent instead of the card, checked against the file on disk. A symbol's body is refs --source

**--to** *node*
: Print the shortest chain of edges from the node to this node (an ID or a name) instead of its card

## Examples

*A target in full*

```sh
magus explain target:pkg/api:build
```

*A doc section's text, by anchor*

```sh
magus explain docsection:docs/reference/cli.md#verbosity --source
```

*Resolve by bare name*

```sh
magus explain build
```

*A spell's context*

```sh
magus explain spell:go
```

*As a record*

```sh
magus explain build -o json
```

*How two projects are related*

```sh
magus explain pkg/api --to pkg/web
```

*From a spell to a diagnostic*

```sh
magus explain spell:go --to MGS3003
```

*The chain as a record*

```sh
magus explain pkg/api --to pkg/web -o json
```

## See Also

[**magus**(1)](magus.md), [**magus-ls**(1)](magus-ls.md), [**magus-describe**(1)](magus-describe.md), [**magus-where**(1)](magus-where.md), [**magus-run**(1)](magus-run.md), [**magus-affected**(1)](magus-affected.md), [**magus-x**(1)](magus-x.md), [**magus-clean**(1)](magus-clean.md), [**magus-shell**(1)](magus-shell.md), [**magus-query**(1)](magus-query.md), [**magus-refs**(1)](magus-refs.md), [**magus-graph**(1)](magus-graph.md), [**magus-diff**(1)](magus-diff.md), [**magus-vcs**(1)](magus-vcs.md), [**magus-session**(1)](magus-session.md), [**magus-job**(1)](magus-job.md), [**magus-notes**(1)](magus-notes.md), [**magus-queue**(1)](magus-queue.md), [**magus-watch**(1)](magus-watch.md), [**magus-events**(1)](magus-events.md), [**magus-server**(1)](magus-server.md), [**magus-broker**(1)](magus-broker.md), [**magus-mcp**(1)](magus-mcp.md), [**magus-status**(1)](magus-status.md), [**magus-buzz**(1)](magus-buzz.md), [**magus-spell**(1)](magus-spell.md), [**magus-agent**(1)](magus-agent.md), [**magus-init**(1)](magus-init.md), [**magus-doctor**(1)](magus-doctor.md), [**magus-config**(1)](magus-config.md), [**magus-self**(1)](magus-self.md), [**magus-version**(1)](magus-version.md)

