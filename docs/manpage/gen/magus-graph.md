---
title: magus graph
description: "Emit the project dependency DAG, export the knowledge graph for external graph tools, and report the graph's shape (god nodes, orphans, doc coverage)."
tags: [cli, magus graph, graph, knowledge graph, dependency graph, export, graphml]
---

# magus-graph

The workspace's graphs as objects: deps, export, stats

## Synopsis

**magus** graph \<deps|export|stats\> [flags]

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

**--global**
: Union the workspaces registered in config (knowledge.workspaces); node IDs are namespaced by workspace

**--refresh**
: Force a full graph rebuild before exporting

**--select** *string*
: Export only the neighborhood of a query (same grammar as magus query); required for -o dot and -o mermaid

### graph stats options

**--global**
: Union the workspaces registered in config (knowledge.workspaces) before computing stats

**--kind** *string*
: Scope every section to one node kind (spell, target, doc, ...)

**--refresh**
: Force a full graph rebuild first

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

## See Also

[**magus**(1)](magus.md), [**magus-ls**(1)](magus-ls.md), [**magus-describe**(1)](magus-describe.md), [**magus-run**(1)](magus-run.md), [**magus-x**(1)](magus-x.md), [**magus-where**(1)](magus-where.md), [**magus-tail**(1)](magus-tail.md), [**magus-affected**(1)](magus-affected.md), [**magus-insight**(1)](magus-insight.md), [**magus-watch**(1)](magus-watch.md), [**magus-status**(1)](magus-status.md), [**magus-doctor**(1)](magus-doctor.md), [**magus-config**(1)](magus-config.md), [**magus-server**(1)](magus-server.md), [**magus-completion**(1)](magus-completion.md), [**magus-init**(1)](magus-init.md), [**magus-self**(1)](magus-self.md), [**magus-version**(1)](magus-version.md)

