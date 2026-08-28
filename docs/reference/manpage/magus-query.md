---
title: magus query
generated_from: internal/cli/registry.go
description: "Resolve search terms to knowledge-graph nodes and return the ranked matches plus their neighborhood; also retrieves a target's captured output by ref and a run's journal by invocation id."
tags: [cli, magus query, query, knowledge graph, search, output, reference, invocation, journal, audit]
---

# magus-query

Search the knowledge graph, and retrieve a run's output or journal by id

## Synopsis

**magus** query \<terms\> [flags]

## Description

Ask the workspace what it knows. query resolves terms to nodes in the
knowledge graph and returns the ranked matches plus the induced subgraph around
them, collected up to a node budget. Its siblings explain and path read the same
graph: explain shows one node's context, path connects two nodes.

Terms are free text plus field filters, and they compose: kind:spell, project:pkg/foo,
relation:uses, id:build, and negation with a leading dash (-kind:op). A bare word
matches names and documentation.

query is also the retrieval verb for the two ids magus prints, each an EXPLICIT
subcommand rather than a shape-routed positional, so a search term can never collide
with an id:

output \<ref\>       One target execution's captured output, by the reference id
                     shown when the target ran (out1a2b3c). The default prints the
                     exact bytes, so it pipes anywhere. --meta shows the run's
                     identity instead - descriptor, lineage, cache key, and the
                     digests of the key's component classes, which is the
                     machine-comparable half of a works-on-my-machine report.
                     --attempts lists the ref's stored executions, --publish uploads
                     the output to the remote cache as a signed bundle, and --open
                     hands the bytes to the browser log viewer in a URL fragment
                     (delivered privately; never uploaded).
  invocation \<id\>    One run's journal, by the invocation id shown as inv: in
                     query output \<ref\> --meta. --secrets narrows it to the
                     credential reads - which references the run reached for and
                     through which provider, never the value - which is how an audit
                     answers "what did this run touch". Run logs are trimmed to a cap
                     by the daemon's RotateLogs job, so this answers for recent runs
                     rather than forever.

The graph is cache-backed under \<cache\>/knowledge and only shards whose sources
changed are rebuilt, so a query is cheap to repeat; --refresh forces a full rebuild.

## Options

**--attempts**
: output \<ref\>: list the ref's stored executions (newest first)

**--budget** *int*
: Max nodes in the returned neighborhood (default 50)

**--global**
: Query across the workspaces registered in config (knowledge.workspaces); IDs are namespaced by workspace

**--kind** *string*
: Restrict matches to these node kinds (comma-separated)

**--meta**
: output \<ref\>: show the run's identity - descriptor, lineage, cache key, component digests

**--open**
: output \<ref\>: open the captured output in the browser log viewer (delivered privately)

**--print**
: With --open, print the viewer URL instead of launching a browser

**--publish**
: output \<ref\>: upload this run's output to the remote cache as a signed bundle

**--refresh**
: Force a full graph rebuild before querying

**--secrets**
: invocation \<id\>: list only the credential reads (reference and provider, never the value)

**--url** *string* (default: https://eli.gladman.cc/magus/console/logs/)
: With --open, base URL of the log viewer page (override for a self-hosted mirror)

## Subcommands

**output**
: Retrieve one target execution's captured output by reference id

**invocation**
: Read one run's journal by invocation id (--secrets for the credential reads)

## Examples

*Find a spell by name*

```sh
magus query kind:spell go
```

*What uses this target*

```sh
magus query relation:uses id:build
```

*Everything but ops*

```sh
magus query docker -kind:op
```

*Print a run's captured output*

```sh
magus query output out1a2b3c
```

*Compare a run's cache key*

```sh
magus query output out1a2b3c --meta
```

*Audit a run's credential reads*

```sh
magus query invocation invmsm3vcou1 --secrets
```

## See Also

[**magus**(1)](magus.md), [**magus-ls**(1)](magus-ls.md), [**magus-describe**(1)](magus-describe.md), [**magus-run**(1)](magus-run.md), [**magus-x**(1)](magus-x.md), [**magus-where**(1)](magus-where.md), [**magus-affected**(1)](magus-affected.md), [**magus-graph**(1)](magus-graph.md), [**magus-explain**(1)](magus-explain.md), [**magus-path**(1)](magus-path.md), [**magus-refs**(1)](magus-refs.md), [**magus-watch**(1)](magus-watch.md), [**magus-events**(1)](magus-events.md), [**magus-status**(1)](magus-status.md), [**magus-clean**(1)](magus-clean.md), [**magus-vcs**(1)](magus-vcs.md), [**magus-doctor**(1)](magus-doctor.md), [**magus-config**(1)](magus-config.md), [**magus-session**(1)](magus-session.md), [**magus-memory**(1)](magus-memory.md), [**magus-notes**(1)](magus-notes.md), [**magus-diff**(1)](magus-diff.md), [**magus-server**(1)](magus-server.md), [**magus-buzz**(1)](magus-buzz.md), [**magus-completion**(1)](magus-completion.md), [**magus-man**(1)](magus-man.md), [**magus-init**(1)](magus-init.md), [**magus-agent**(1)](magus-agent.md), [**magus-self**(1)](magus-self.md), [**magus-version**(1)](magus-version.md)

