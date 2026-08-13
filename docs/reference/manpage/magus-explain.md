---
title: magus explain
description: "Show a single knowledge-graph node in context: its data, its incoming and outgoing edges with provenance, and how many nodes reach it."
tags: [cli, magus explain, explain, knowledge graph, node, edges, provenance, impact]
---

# magus-explain

Show one knowledge-graph node's context: data, edges, and reach

## Synopsis

**magus** explain \<node-id-or-name\> [flags]

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

The graph is cache-backed under \<cache\>/knowledge and only shards whose sources
changed are rebuilt; --refresh forces a full rebuild.

## Options

**--global**
: Resolve across the workspaces registered in config (knowledge.workspaces)

**--refresh**
: Force a full graph rebuild before explaining

## Examples

*A target in full*

```sh
magus explain target:pkg/api:build
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

## See Also

[**magus**(1)](magus.md), [**magus-ls**(1)](magus-ls.md), [**magus-describe**(1)](magus-describe.md), [**magus-run**(1)](magus-run.md), [**magus-x**(1)](magus-x.md), [**magus-where**(1)](magus-where.md), [**magus-affected**(1)](magus-affected.md), [**magus-graph**(1)](magus-graph.md), [**magus-query**(1)](magus-query.md), [**magus-path**(1)](magus-path.md), [**magus-refs**(1)](magus-refs.md), [**magus-watch**(1)](magus-watch.md), [**magus-status**(1)](magus-status.md), [**magus-clean**(1)](magus-clean.md), [**magus-vcs**(1)](magus-vcs.md), [**magus-doctor**(1)](magus-doctor.md), [**magus-config**(1)](magus-config.md), [**magus-memory**(1)](magus-memory.md), [**magus-server**(1)](magus-server.md), [**magus-buzz**(1)](magus-buzz.md), [**magus-completion**(1)](magus-completion.md), [**magus-man**(1)](magus-man.md), [**magus-init**(1)](magus-init.md), [**magus-agent**(1)](magus-agent.md), [**magus-hook**(1)](magus-hook.md), [**magus-notify**(1)](magus-notify.md), [**magus-self**(1)](magus-self.md), [**magus-version**(1)](magus-version.md)

