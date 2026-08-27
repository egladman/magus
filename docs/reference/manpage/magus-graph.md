---
title: magus graph
generated_from: internal/cli/registry.go
description: "Emit the project dependency DAG, export the knowledge graph for external graph tools, and report the graph's shape (god nodes, orphans, doc coverage)."
tags: [cli, magus graph, graph, knowledge graph, dependency graph, export, graphml]
---

# magus-graph

The workspace's graphs as objects: deps, export, stats

## Synopsis

**magus** graph \<deps|export|stats|open\> [flags]

## Description

The workspace's graphs as objects: emit, export, and measure them. The
query, explain, and path verbs read the knowledge graph; magus graph is the
home of the graph itself.

Subcommands (the first argument):

deps     The project dependency DAG. A trailing list of project paths roots
           the graph; -o selects text, json, yaml, dot, mermaid, or tree. The
           same view scoped to a run is available as magus run \<target\> --graph
           and magus affected \<target\> --graph.
  export   The merged knowledge graph: the deterministic, cache-backed graph of
           the magus domain (projects, targets, spells, ops, charms, modules,
           methods, diagnostics, docs, buzz sources). -o json emits the
           node-link form; -o graphml emits GraphML. External graph viewers
           (Gephi, yEd) read both directly. --select "\<terms\>" narrows the
           export to a query's neighborhood (same engine as magus query); -o dot
           and -o mermaid render only with --select, since the full graph has too
           many nodes to lay out. The graph is cache-backed under
           \<cache\>/knowledge; only shards whose sources changed are rebuilt.
  stats    The graph's shape: god nodes (the most connected spells, modules,
           targets - where structural risk concentrates), orphans (docs that
           document nothing, spells no target uses), and doc coverage (the
           share of diagnostics, spells, and modules with a doc). --kind scopes
           every section to one node kind. insight report embeds this section.
  open     Open the workspace's knowledge graph (or target dependency graph with
           --targets) in the hosted, interactive Graph Explorer. The graph is
           delivered privately: by default it rides in the URL fragment
           (#data=...), which browsers never send to a server; --serve instead
           hands it from an ephemeral 127.0.0.1 loopback server (no size limit).

### graph deps options

**--depth** *int*
: Cap displayed depth (0 = unlimited)

**--spell** *string*
: Only projects driven by this spell

**--target** *string*
: Target whose duration history annotates nodes (default: build)

**--upstream**
: Show dependents instead of dependencies

### graph export options

**--budget** *int* (default: 50)
: Node budget for --select (how many nodes the neighborhood may collect)

**--follow**
: With --open: keep the explorer updating from the running daemon instead of showing a snapshot (needs magus server start)

**--global**
: Union the workspaces registered in config (knowledge.workspaces); node IDs are namespaced by workspace

**--open**
: Deliver the graph to the hosted Graph Explorer instead of stdout; it never leaves your machine

**--print**
: With --open: print the explorer URL to stdout instead of launching a browser

**--refresh**
: Force a full graph rebuild before exporting

**--reproducible**
: Omit everything that is not a function of the source tree (locally observed runtime attrs, git history), so two checkouts of one commit export identical bytes

**--select** *string*
: Export only the neighborhood of a query (same grammar as magus query); required for -o dot and -o mermaid

**--serve**
: With --open: hand the graph to the page from an ephemeral loopback server instead of a URL fragment (no size limit; incompatible with --targets)

**--static**
: Deprecated alias for --reproducible

**--targets**
: With --open: open the target dependency graph instead of the knowledge graph; pass a project path to scope it

**--url** *string* (default: https://eli.gladman.cc/magus/console/graph/)
: With --open: base URL of the Graph Explorer page (override for a self-hosted mirror)

### graph stats options

**--global**
: Union the workspaces registered in config (knowledge.workspaces) before computing stats

**--kind** *string*
: Scope every section to one node kind (spell, target, doc, ...)

**--refresh**
: Force a full graph rebuild first

**--symbols**
: Include the lazily-loaded symbol shards in the stats; excluded by default because they can dwarf the domain graph

## Subcommands

**deps**
: Emit the project dependency DAG (text, json, yaml, dot, mermaid, tree)

**export**
: Export the merged knowledge graph (json node-link or graphml)

**stats**
: Report the knowledge graph's shape: god nodes, orphans, doc coverage

## Examples

*Project DAG as Mermaid*

```sh
magus graph deps -o mermaid
```

*DAG rooted at one project, dependents up*

```sh
magus graph deps pkg/api --upstream
```

*Knowledge graph for an external viewer*

```sh
magus graph export -o json > graph.json
```

*GraphML for Gephi or yEd*

```sh
magus graph export -o graphml > graph.graphml
```

*A query's neighborhood as Mermaid*

```sh
magus graph export --select 'kind:spell go' -o mermaid
```

*Where structural risk concentrates*

```sh
magus graph stats
```

*Doc coverage for spells only*

```sh
magus graph stats --kind spell
```

*Open knowledge graph in browser*

```sh
magus graph export --open
```

*Open target dependency graph*

```sh
magus graph export --open --targets
```

*Scope target graph to one project*

```sh
magus graph export --open --targets docs
```

*Print the URL instead of opening*

```sh
magus graph export --open --targets --print
```

## See Also

[**magus**(1)](magus.md), [**magus-ls**(1)](magus-ls.md), [**magus-describe**(1)](magus-describe.md), [**magus-run**(1)](magus-run.md), [**magus-x**(1)](magus-x.md), [**magus-where**(1)](magus-where.md), [**magus-affected**(1)](magus-affected.md), [**magus-query**(1)](magus-query.md), [**magus-explain**(1)](magus-explain.md), [**magus-path**(1)](magus-path.md), [**magus-refs**(1)](magus-refs.md), [**magus-watch**(1)](magus-watch.md), [**magus-status**(1)](magus-status.md), [**magus-clean**(1)](magus-clean.md), [**magus-vcs**(1)](magus-vcs.md), [**magus-doctor**(1)](magus-doctor.md), [**magus-config**(1)](magus-config.md), [**magus-session**(1)](magus-session.md), [**magus-memory**(1)](magus-memory.md), [**magus-notes**(1)](magus-notes.md), [**magus-diff**(1)](magus-diff.md), [**magus-server**(1)](magus-server.md), [**magus-buzz**(1)](magus-buzz.md), [**magus-completion**(1)](magus-completion.md), [**magus-man**(1)](magus-man.md), [**magus-init**(1)](magus-init.md), [**magus-agent**(1)](magus-agent.md), [**magus-self**(1)](magus-self.md), [**magus-version**(1)](magus-version.md)

