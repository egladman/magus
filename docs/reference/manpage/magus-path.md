---
title: magus path
generated_from: internal/cli/registry.go
description: "Find the shortest chain of edges connecting two knowledge-graph nodes, walking edges in either direction, with each hop's relation named."
tags: [cli, magus path, path, knowledge graph, shortest path, relationship, coupling]
---

# magus-path

Connect two knowledge-graph nodes: the shortest chain of edges between them

## Synopsis

**magus** path \<a\> \<b\> [flags]

## Description

path connects two nodes: the shortest chain of edges between them (edges
walked in either direction), with each hop's relation.

It answers the question the other two verbs cannot: not "what is this" or "what does
this touch", but "how are these two related at all". That is the question worth asking
when a change to one thing breaks a seemingly unrelated other, or when you suspect two
projects are coupled and want the chain rather than a hunch. Each hop names its
relation, so the answer is a mechanism you can check and not just a claim that a
connection exists.

Edges are walked in EITHER direction, which is what makes it useful and is also its
main caveat: a returned chain proves the two nodes are connected in the graph, not that
influence flows from one to the other. Read the hop relations to see which way each
link actually points.

Each argument is a node ID (target:pkg/foo:build) or a name that resolves to one.
The graph is cache-backed under \<cache\>/knowledge; --refresh forces a full rebuild.

## Options

**--global**
: Resolve endpoints across the workspaces registered in config (knowledge.workspaces)

**--refresh**
: Force a full graph rebuild before pathfinding

## Examples

*How are two projects related*

```sh
magus path pkg/api pkg/web
```

*From a spell to a diagnostic*

```sh
magus path spell:go MGS3003
```

*Between two targets by ID*

```sh
magus path target:pkg/api:build target:pkg/web:test
```

*As a record*

```sh
magus path pkg/api pkg/web -o json
```

## See Also

[**magus**(1)](magus.md), [**magus-ls**(1)](magus-ls.md), [**magus-describe**(1)](magus-describe.md), [**magus-run**(1)](magus-run.md), [**magus-x**(1)](magus-x.md), [**magus-where**(1)](magus-where.md), [**magus-affected**(1)](magus-affected.md), [**magus-graph**(1)](magus-graph.md), [**magus-query**(1)](magus-query.md), [**magus-explain**(1)](magus-explain.md), [**magus-refs**(1)](magus-refs.md), [**magus-watch**(1)](magus-watch.md), [**magus-events**(1)](magus-events.md), [**magus-status**(1)](magus-status.md), [**magus-clean**(1)](magus-clean.md), [**magus-vcs**(1)](magus-vcs.md), [**magus-doctor**(1)](magus-doctor.md), [**magus-config**(1)](magus-config.md), [**magus-session**(1)](magus-session.md), [**magus-memory**(1)](magus-memory.md), [**magus-notes**(1)](magus-notes.md), [**magus-diff**(1)](magus-diff.md), [**magus-server**(1)](magus-server.md), [**magus-buzz**(1)](magus-buzz.md), [**magus-completion**(1)](magus-completion.md), [**magus-man**(1)](magus-man.md), [**magus-init**(1)](magus-init.md), [**magus-agent**(1)](magus-agent.md), [**magus-self**(1)](magus-self.md), [**magus-version**(1)](magus-version.md)

