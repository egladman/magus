---
title: GraphService
generated_from: reference/api/
description: "GraphService answers the questions the CLI's query/explain/path/stats verbs answer, over the same knowledge graph."
tags: [api, proto, connect, grpc, graphservice]
---

# GraphService

GraphService answers the questions the CLI's query/explain/path/stats verbs answer, over the same knowledge graph. It exists so the browser stops reimplementing them: the Graph Explorer's filter was a second, divergent copy of the query grammar, scoring by raw degree over a payload the daemon had already sent whole.

Every verb is read-only, so the daemon mounts the service behind the console read bearer.

The definition and schema\_version fields every domain output carries are deliberately absent here. The proto package IS the version and buf-breaking gates it, so a second version number could only ever disagree with the first; definition is CLI help prose, re-sent on every response to a typed client that already knows what it called.

GET /api/v1/graph is NOT superseded. It is the bulk subgraph fetch - a whole document - which is a different job from ranked retrieval, and the page already speaks it.

Package `magus.graph.v1alpha1`, defined in `proto/magus/graph/v1alpha1/graph.proto`. Source: [graph.proto:62](https://github.com/egladman/magus/blob/main/proto/magus/graph/v1alpha1/graph.proto#L62). Part of the [daemon API](../../index.md).

## Methods

### QueryNodes

QueryNodes resolves search terms to ranked matches plus the induced neighborhood, collected up to a node budget. Paginated: page with offset + len(matches) against match\_count, which is the TOTAL, not the page size.

`POST /magus.graph.v1alpha1.GraphService/QueryNodes`: unary. Source: [graph.proto:66](https://github.com/egladman/magus/blob/main/proto/magus/graph/v1alpha1/graph.proto#L66).

Takes [QueryNodesRequest](#querynodesrequest), returns [QueryNodesResponse](#querynodesresponse).

### ResolveNodes

ResolveNodes returns the ranked candidates for a partial reference, for completion. Cheaper than QueryNodes: matches only, no neighborhood.

`POST /magus.graph.v1alpha1.GraphService/ResolveNodes`: unary. Source: [graph.proto:69](https://github.com/egladman/magus/blob/main/proto/magus/graph/v1alpha1/graph.proto#L69).

Takes [ResolveNodesRequest](#resolvenodesrequest), returns [ResolveNodesResponse](#resolvenodesresponse).

### ExplainNode

ExplainNode returns one node's context: its data, its in/out edges with provenance, and how many nodes transitively reach it. A name that resolves to nothing is NOT\_FOUND.

`POST /magus.graph.v1alpha1.GraphService/ExplainNode`: unary. Source: [graph.proto:72](https://github.com/egladman/magus/blob/main/proto/magus/graph/v1alpha1/graph.proto#L72).

Takes [ExplainNodeRequest](#explainnoderequest), returns [NodeContext](#nodecontext).

### FindPath

FindPath returns the shortest chain between two nodes, edges walked in either direction. found=false is an answer, not an error; an endpoint that resolves to nothing is NOT\_FOUND.

`POST /magus.graph.v1alpha1.GraphService/FindPath`: unary. Source: [graph.proto:75](https://github.com/egladman/magus/blob/main/proto/magus/graph/v1alpha1/graph.proto#L75).

Takes [FindPathRequest](#findpathrequest), returns [Path](#path).

### GetGraphStats

GetGraphStats returns where the workspace concentrates, neglects, and fragments.

`POST /magus.graph.v1alpha1.GraphService/GetGraphStats`: unary. Source: [graph.proto:77](https://github.com/egladman/magus/blob/main/proto/magus/graph/v1alpha1/graph.proto#L77).

Takes [GetGraphStatsRequest](#getgraphstatsrequest), returns [GraphStats](#graphstats).

## Messages

### Answer

Answer classifies a result against what magus could actually search. A stated reason or any gap makes the verdict unknown WHETHER OR NOT the lookup matched - that is the difference from a plain emptiness check, and it is why a bare term matching nothing says nothing about whether a code symbol by that name exists.

Source: [graph.proto:215](https://github.com/egladman/magus/blob/main/proto/magus/graph/v1alpha1/graph.proto#L215).

| Field | Type | # | Description |
|-------|------|---|-------------|
| `verdict` | string | 1 |  |
| `reason` | string | 2 |  |
| `gaps` | [repeated SymbolGap](#symbolgap) | 3 |  |

Used by: [QueryNodes (response)](graph.md#querynodes).

### DocCoverage

DocCoverage is doc coverage for one documentable kind. undocumented is a capped sample, not the full set.

Source: [graph.proto:203](https://github.com/egladman/magus/blob/main/proto/magus/graph/v1alpha1/graph.proto#L203).

| Field | Type | # | Description |
|-------|------|---|-------------|
| `kind` | string | 1 |  |
| `total` | int32 | 2 |  |
| `documented` | int32 | 3 |  |
| `percent` | int32 | 4 |  |
| `undocumented` | repeated string | 5 |  |

Used by: [GetGraphStats (response)](graph.md#getgraphstats).

### Edge

Edge is one directed graph edge (a "link").

Source: [graph.proto:39](https://github.com/egladman/magus/blob/main/proto/magus/graph/v1alpha1/graph.proto#L39).

| Field | Type | # | Description |
|-------|------|---|-------------|
| `source` | string | 1 |  |
| `target` | string | 2 |  |
| `relation` | string | 3 |  |
| `confidence` | string | 4 |  |
| `score` | double | 5 |  |
| `provenance` | string | 6 |  |

Used by: [QueryNodes (response)](graph.md#querynodes).

### EdgeRef

EdgeRef is one edge seen FROM a focus node, so direction is relative to that node.

Source: [graph.proto:131](https://github.com/egladman/magus/blob/main/proto/magus/graph/v1alpha1/graph.proto#L131).

| Field | Type | # | Description |
|-------|------|---|-------------|
| `relation` | string | 1 |  |
| `direction` | [EdgeDirection](#edgedirection) | 2 |  |
| `other` | string | 3 |  |
| `other_kind` | string | 4 |  |
| `other_label` | string | 5 |  |
| `provenance` | string | 6 |  |

Used by: [ExplainNode (response)](graph.md#explainnode).

### ExplainNodeRequest

Source: [graph.proto:119](https://github.com/egladman/magus/blob/main/proto/magus/graph/v1alpha1/graph.proto#L119).

| Field | Type | # | Description |
|-------|------|---|-------------|
| `name` | string | 1 | a node id, or any reference ResolveNodes accepts |

Used by: [ExplainNode (request)](graph.md#explainnode).

### FindPathRequest

Source: [graph.proto:146](https://github.com/egladman/magus/blob/main/proto/magus/graph/v1alpha1/graph.proto#L146).

| Field | Type | # | Description |
|-------|------|---|-------------|
| `from` | string | 1 |  |
| `to` | string | 2 |  |

Used by: [FindPath (request)](graph.md#findpath).

### GetGraphStatsRequest

Source: [graph.proto:167](https://github.com/egladman/magus/blob/main/proto/magus/graph/v1alpha1/graph.proto#L167).

| Field | Type | # | Description |
|-------|------|---|-------------|
| `kind` | string | 1 | optional filter; empty = all kinds |

Used by: [GetGraphStats (request)](graph.md#getgraphstats).

### GodNode

Source: [graph.proto:182](https://github.com/egladman/magus/blob/main/proto/magus/graph/v1alpha1/graph.proto#L182).

| Field | Type | # | Description |
|-------|------|---|-------------|
| `id` | string | 1 |  |
| `kind` | string | 2 |  |
| `label` | string | 3 |  |
| `degree` | int32 | 4 | in + out |
| `in` | int32 | 5 |  |
| `out` | int32 | 6 |  |

Used by: [GetGraphStats (response)](graph.md#getgraphstats).

### GraphStats

Source: [graph.proto:171](https://github.com/egladman/magus/blob/main/proto/magus/graph/v1alpha1/graph.proto#L171).

| Field | Type | # | Description |
|-------|------|---|-------------|
| `node_count` | int32 | 1 |  |
| `edge_count` | int32 | 2 |  |
| `gods` | [repeated GodNode](#godnode) | 3 |  |
| `orphans` | [repeated Orphan](#orphan) | 4 |  |
| `coverage` | [repeated DocCoverage](#doccoverage) | 5 |  |
| `isolated_count` | int32 | 6 |  |
| `component_count` | int32 | 7 |  |
| `largest_component_size` | int32 | 8 |  |

Used by: [GetGraphStats (response)](graph.md#getgraphstats).

### Match

Match is one ranked node. staleness/outrun\_days carry the EVIDENCE for a prose match that ranked down because the thing it describes moved on without it, so the weight is never silent. Empty on anything not penalized.

Source: [graph.proto:110](https://github.com/egladman/magus/blob/main/proto/magus/graph/v1alpha1/graph.proto#L110).

| Field | Type | # | Description |
|-------|------|---|-------------|
| `id` | string | 1 |  |
| `kind` | string | 2 |  |
| `label` | string | 3 |  |
| `score` | int32 | 4 |  |
| `staleness` | string | 5 |  |
| `outrun_days` | int32 | 6 |  |

Used by: [QueryNodes (response)](graph.md#querynodes), [ResolveNodes (response)](graph.md#resolvenodes).

### Node

Node is one graph node.

Source: [graph.proto:29](https://github.com/egladman/magus/blob/main/proto/magus/graph/v1alpha1/graph.proto#L29).

| Field | Type | # | Description |
|-------|------|---|-------------|
| `id` | string | 1 |  |
| `kind` | string | 2 |  |
| `label` | string | 3 |  |
| `doc` | string | 4 |  |
| `source` | string | 5 | path or path:line provenance |
| `attrs` | map<string, string> | 6 | kind-specific (charm pointer, MGS URL, ...) |

Used by: [ExplainNode (response)](graph.md#explainnode), [QueryNodes (response)](graph.md#querynodes).

### NodeContext

Source: [graph.proto:123](https://github.com/egladman/magus/blob/main/proto/magus/graph/v1alpha1/graph.proto#L123).

| Field | Type | # | Description |
|-------|------|---|-------------|
| `node` | [Node](#node) | 1 |  |
| `blast_radius` | int32 | 2 |  |
| `out` | [repeated EdgeRef](#edgeref) | 3 |  |
| `in` | [repeated EdgeRef](#edgeref) | 4 |  |

Used by: [ExplainNode (response)](graph.md#explainnode).

### Orphan

Orphan is a node missing the connection its KIND implies - a doc that documents nothing, a spell no target uses - with the reason in plain English. Not the same as "no edges at all": the reason is what makes it actionable.

Source: [graph.proto:194](https://github.com/egladman/magus/blob/main/proto/magus/graph/v1alpha1/graph.proto#L194).

| Field | Type | # | Description |
|-------|------|---|-------------|
| `id` | string | 1 |  |
| `kind` | string | 2 |  |
| `label` | string | 3 |  |
| `reason` | string | 4 |  |

Used by: [GetGraphStats (response)](graph.md#getgraphstats).

### Path

Source: [graph.proto:151](https://github.com/egladman/magus/blob/main/proto/magus/graph/v1alpha1/graph.proto#L151).

| Field | Type | # | Description |
|-------|------|---|-------------|
| `from` | string | 1 |  |
| `to` | string | 2 |  |
| `found` | bool | 3 |  |
| `steps` | [repeated PathStep](#pathstep) | 4 |  |

Used by: [FindPath (response)](graph.md#findpath).

### PathStep

PathStep is one hop as WALKED (from -> to). forward=false means the path traversed the underlying edge against its own direction.

Source: [graph.proto:160](https://github.com/egladman/magus/blob/main/proto/magus/graph/v1alpha1/graph.proto#L160).

| Field | Type | # | Description |
|-------|------|---|-------------|
| `from` | string | 1 |  |
| `to` | string | 2 |  |
| `relation` | string | 3 |  |
| `forward` | bool | 4 |  |

Used by: [FindPath (response)](graph.md#findpath).

### QueryNodesRequest

Source: [graph.proto:80](https://github.com/egladman/magus/blob/main/proto/magus/graph/v1alpha1/graph.proto#L80).

| Field | Type | # | Description |
|-------|------|---|-------------|
| `query` | string | 1 | the magus query grammar, verbatim |
| `budget` | int32 | 2 | neighborhood node budget; 0 = server default |
| `offset` | int32 | 3 |  |
| `page_size` | int32 | 4 | 0 = every match from offset on |

Used by: [QueryNodes (request)](graph.md#querynodes).

### QueryNodesResponse

Source: [graph.proto:87](https://github.com/egladman/magus/blob/main/proto/magus/graph/v1alpha1/graph.proto#L87).

| Field | Type | # | Description |
|-------|------|---|-------------|
| `query` | string | 1 |  |
| `budget` | int32 | 2 |  |
| `match_count` | int32 | 3 | TOTAL matches, not this page |
| `offset` | int32 | 4 |  |
| `matches` | [repeated Match](#match) | 5 |  |
| `nodes` | [repeated Node](#node) | 6 |  |
| `links` | [repeated Edge](#edge) | 7 |  |
| `answer` | [Answer](#answer) | 8 |  |

Used by: [QueryNodes (response)](graph.md#querynodes).

### ResolveNodesRequest

Source: [graph.proto:98](https://github.com/egladman/magus/blob/main/proto/magus/graph/v1alpha1/graph.proto#L98).

| Field | Type | # | Description |
|-------|------|---|-------------|
| `reference` | string | 1 |  |
| `limit` | int32 | 2 |  |

Used by: [ResolveNodes (request)](graph.md#resolvenodes).

### ResolveNodesResponse

Source: [graph.proto:103](https://github.com/egladman/magus/blob/main/proto/magus/graph/v1alpha1/graph.proto#L103).

| Field | Type | # | Description |
|-------|------|---|-------------|
| `matches` | [repeated Match](#match) | 1 |  |

Used by: [ResolveNodes (response)](graph.md#resolvenodes).

### SymbolGap

SymbolGap is one project whose declared symbol index magus could not read: the evidence behind an unknown verdict. The project is flattened to its two wire fields rather than nested, because ProjectRef's third field is an absolute host path that never leaves the daemon.

Source: [graph.proto:224](https://github.com/egladman/magus/blob/main/proto/magus/graph/v1alpha1/graph.proto#L224).

| Field | Type | # | Description |
|-------|------|---|-------------|
| `project_path` | string | 1 | workspace-relative; "." is the root |
| `project_name` | string | 2 | the human label, which differs from the path only at the root |
| `state` | string | 3 |  |
| `detail` | string | 4 |  |

Used by: [QueryNodes (response)](graph.md#querynodes).

## Enums

### EdgeDirection

Source: [graph.proto:140](https://github.com/egladman/magus/blob/main/proto/magus/graph/v1alpha1/graph.proto#L140).

| Value | # | Description |
|-------|---|-------------|
| `EDGE_DIRECTION_UNSPECIFIED` | 0 |  |
| `EDGE_DIRECTION_OUT` | 1 | the focus node is the edge's source |
| `EDGE_DIRECTION_IN` | 2 | the focus node is the edge's target |

Used by: [ExplainNode (response)](graph.md#explainnode).

