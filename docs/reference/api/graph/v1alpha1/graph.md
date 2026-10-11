---
title: GraphService
generated_from: reference/api/
description: "GraphService answers the questions the CLI's query/explain/path/stats verbs answer, over the same knowledge graph."
tags: [api, proto, connect, grpc, graphservice]
---

# GraphService

GraphService answers the questions the CLI's query/explain/path/stats verbs answer, over the same knowledge graph. It exists so the browser stops reimplementing them: the Graph Explorer's filter was a second, divergent copy of the query grammar, scoring by raw degree over a payload the server had already sent whole.

Every verb is read-only, so the server mounts the service behind the console read bearer.

The definition and schema\_version fields every domain output carries are deliberately absent here. The proto package IS the version and buf-breaking gates it, so a second version number could only ever disagree with the first; definition is CLI help prose, re-sent on every response to a typed client that already knows what it called.

GET /api/v1/graph is NOT superseded. It is the bulk subgraph fetch - a whole document - which is a different job from ranked retrieval, and the page already speaks it.

Package `magus.graph.v1alpha1`, defined in `proto/magus/graph/v1alpha1/graph.proto`. Source: [graph.proto:63](https://github.com/egladman/magus/blob/main/proto/magus/graph/v1alpha1/graph.proto#L63). Part of the [daemon API](../../index.md).

## Methods

### QueryNodes

QueryNodes resolves search terms to ranked matches plus the induced neighborhood, collected up to a node budget. Paginated: page with offset + len(matches) against match\_count, which is the TOTAL, not the page size.

`POST /magus.graph.v1alpha1.GraphService/QueryNodes`: unary. Source: [graph.proto:67](https://github.com/egladman/magus/blob/main/proto/magus/graph/v1alpha1/graph.proto#L67).

Takes [QueryNodesRequest](#querynodesrequest), returns [QueryNodesResponse](#querynodesresponse).

### ResolveNodes

ResolveNodes returns the ranked candidates for a partial reference, for completion. Cheaper than QueryNodes: matches only, no neighborhood.

`POST /magus.graph.v1alpha1.GraphService/ResolveNodes`: unary. Source: [graph.proto:70](https://github.com/egladman/magus/blob/main/proto/magus/graph/v1alpha1/graph.proto#L70).

Takes [ResolveNodesRequest](#resolvenodesrequest), returns [ResolveNodesResponse](#resolvenodesresponse).

### ExplainNode

ExplainNode returns one node's context: its data, its in/out edges with provenance, and how many nodes transitively reach it. A name that resolves to nothing is NOT\_FOUND.

`POST /magus.graph.v1alpha1.GraphService/ExplainNode`: unary. Source: [graph.proto:73](https://github.com/egladman/magus/blob/main/proto/magus/graph/v1alpha1/graph.proto#L73).

Takes [ExplainNodeRequest](#explainnoderequest), returns [NodeContext](#nodecontext).

### FindPath

FindPath returns the shortest chain between two nodes, edges walked in either direction. found=false is an answer, not an error; an endpoint that resolves to nothing is NOT\_FOUND.

`POST /magus.graph.v1alpha1.GraphService/FindPath`: unary. Source: [graph.proto:76](https://github.com/egladman/magus/blob/main/proto/magus/graph/v1alpha1/graph.proto#L76).

Takes [FindPathRequest](#findpathrequest), returns [Path](#path).

### FindDependents

FindDependents returns every node that transitively DEPENDS ON one, as ids - the answer to "what rebuilds if I change this".

Deliberately not NodeContext.blast\_radius, which is a different question wearing a similar name: that counts everything reaching a node by ANY relation. Nothing depends\_on a spell (a target USES one), so a spell's blast\_radius runs to the hundreds while its dependents are empty, and both are right. Ids rather than a count because the caller highlights them; a separate RPC rather than a field on NodeContext because a hub's list is long and an explain card should not carry it.

`POST /magus.graph.v1alpha1.GraphService/FindDependents`: unary. Source: [graph.proto:86](https://github.com/egladman/magus/blob/main/proto/magus/graph/v1alpha1/graph.proto#L86).

Takes [FindDependentsRequest](#finddependentsrequest), returns [Dependents](#dependents).

### FindAffected

FindAffected returns the projects a VCS diff reaches, as graph node ids, so a viewer can highlight what the working tree touches. `magus affected` over the wire.

Read-only like the rest of the service, but the only verb here that reads the VCS rather than the graph, so it is the only one whose answer changes while the graph stands still.

`POST /magus.graph.v1alpha1.GraphService/FindAffected`: unary. Source: [graph.proto:92](https://github.com/egladman/magus/blob/main/proto/magus/graph/v1alpha1/graph.proto#L92).

Takes [FindAffectedRequest](#findaffectedrequest), returns [Affected](#affected).

### GetGraphStats

GetGraphStats returns where the workspace concentrates, neglects, and fragments.

`POST /magus.graph.v1alpha1.GraphService/GetGraphStats`: unary. Source: [graph.proto:94](https://github.com/egladman/magus/blob/main/proto/magus/graph/v1alpha1/graph.proto#L94).

Takes [GetGraphStatsRequest](#getgraphstatsrequest), returns [GraphStats](#graphstats).

## Messages

### Affected

Affected is the reach of one VCS diff: which projects a change forces work in.

Source: [graph.proto:191](https://github.com/egladman/magus/blob/main/proto/magus/graph/v1alpha1/graph.proto#L191).

| Field           | Type            | # | Description                                                                                                                                                                                                                                                 |
| --------------- | --------------- | - | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `base`          | string          | 1 | the ref actually diffed against, after resolution                                                                                                                                                                                                           |
| `changed_files` | int32           | 2 | how many paths the diff carried                                                                                                                                                                                                                             |
| `ids`           | repeated string | 3 | project node ids in the transitive reverse closure, sorted                                                                                                                                                                                                  |
| `fallback`      | string          | 4 | fallback is why the answer is not definitive: a shallow clone, no VCS, an unreadable base. ids is empty whenever it is set, and the two are read together - an empty ids with no fallback means the diff genuinely reaches nothing, which is a real answer. |

Used by: [FindAffected (response)](graph.md#findaffected).

### Answer

Answer classifies a result against what magus could actually search. A stated reason or any gap makes the verdict unknown WHETHER OR NOT the lookup matched - that is the difference from a plain emptiness check, and it is why a bare term matching nothing says nothing about whether a code symbol by that name exists.

Source: [graph.proto:265](https://github.com/egladman/magus/blob/main/proto/magus/graph/v1alpha1/graph.proto#L265).

| Field     | Type                             | # | Description |
| --------- | -------------------------------- | - | ----------- |
| `verdict` | string                           | 1 |             |
| `reason`  | string                           | 2 |             |
| `gaps`    | [repeated SymbolGap](#symbolgap) | 3 |             |

Used by: [QueryNodes (response)](graph.md#querynodes).

### Dependents

Dependents is the transitive depends\_on fan-in of one node. Ids only: a caller that wants a label already has the node, or can ask ExplainNode for the one it cares about.

Source: [graph.proto:178](https://github.com/egladman/magus/blob/main/proto/magus/graph/v1alpha1/graph.proto#L178).

| Field  | Type            | # | Description                                                        |
| ------ | --------------- | - | ------------------------------------------------------------------ |
| `node` | string          | 1 | the resolved node the walk started from                            |
| `ids`  | repeated string | 2 | everything that transitively depends on it; empty is a real answer |

Used by: [FindDependents (response)](graph.md#finddependents).

### DocCoverage

DocCoverage is doc coverage for one documentable kind. undocumented is a capped sample, not the full set.

Source: [graph.proto:253](https://github.com/egladman/magus/blob/main/proto/magus/graph/v1alpha1/graph.proto#L253).

| Field          | Type            | # | Description |
| -------------- | --------------- | - | ----------- |
| `kind`         | string          | 1 |             |
| `total`        | int32           | 2 |             |
| `documented`   | int32           | 3 |             |
| `percent`      | int32           | 4 |             |
| `undocumented` | repeated string | 5 |             |

Used by: [GetGraphStats (response)](graph.md#getgraphstats).

### Edge

Edge is one directed graph edge (a "link").

Source: [graph.proto:39](https://github.com/egladman/magus/blob/main/proto/magus/graph/v1alpha1/graph.proto#L39).

| Field        | Type                | # | Description                                           |
| ------------ | ------------------- | - | ----------------------------------------------------- |
| `source`     | string              | 1 |                                                       |
| `target`     | string              | 2 |                                                       |
| `relation`   | string              | 3 |                                                       |
| `confidence` | string              | 4 |                                                       |
| `score`      | double              | 5 |                                                       |
| `provenance` | string              | 6 |                                                       |
| `attrs`      | map<string, string> | 7 | relation-specific (transport on a declared call, ...) |

Used by: [QueryNodes (response)](graph.md#querynodes).

### EdgeRef

EdgeRef is one edge seen FROM a focus node, so direction is relative to that node.

Source: [graph.proto:152](https://github.com/egladman/magus/blob/main/proto/magus/graph/v1alpha1/graph.proto#L152).

| Field         | Type                            | # | Description |
| ------------- | ------------------------------- | - | ----------- |
| `relation`    | string                          | 1 |             |
| `direction`   | [EdgeDirection](#edgedirection) | 2 |             |
| `other`       | string                          | 3 |             |
| `other_kind`  | string                          | 4 |             |
| `other_label` | string                          | 5 |             |
| `provenance`  | string                          | 6 |             |

Used by: [ExplainNode (response)](graph.md#explainnode).

### ExplainNodeRequest

Source: [graph.proto:136](https://github.com/egladman/magus/blob/main/proto/magus/graph/v1alpha1/graph.proto#L136).

| Field  | Type   | # | Description                                      |
| ------ | ------ | - | ------------------------------------------------ |
| `name` | string | 1 | a node id, or any reference ResolveNodes accepts |

Used by: [ExplainNode (request)](graph.md#explainnode).

### FindAffectedRequest

Source: [graph.proto:183](https://github.com/egladman/magus/blob/main/proto/magus/graph/v1alpha1/graph.proto#L183).

| Field  | Type   | # | Description                                                                                                                                                                                         |
| ------ | ------ | - | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `base` | string | 1 | base is the VCS ref to diff against. Empty takes the workspace's configured base - the same resolution `magus affected` uses - so a caller with no opinion gets the one the repo already agreed on. |

Used by: [FindAffected (request)](graph.md#findaffected).

### FindDependentsRequest

Source: [graph.proto:172](https://github.com/egladman/magus/blob/main/proto/magus/graph/v1alpha1/graph.proto#L172).

| Field  | Type   | # | Description                                      |
| ------ | ------ | - | ------------------------------------------------ |
| `name` | string | 1 | a node id, or any reference ResolveNodes accepts |

Used by: [FindDependents (request)](graph.md#finddependents).

### FindPathRequest

Source: [graph.proto:167](https://github.com/egladman/magus/blob/main/proto/magus/graph/v1alpha1/graph.proto#L167).

| Field  | Type   | # | Description |
| ------ | ------ | - | ----------- |
| `from` | string | 1 |             |
| `to`   | string | 2 |             |

Used by: [FindPath (request)](graph.md#findpath).

### GetGraphStatsRequest

Source: [graph.proto:217](https://github.com/egladman/magus/blob/main/proto/magus/graph/v1alpha1/graph.proto#L217).

| Field  | Type   | # | Description                        |
| ------ | ------ | - | ---------------------------------- |
| `kind` | string | 1 | optional filter; empty = all kinds |

Used by: [GetGraphStats (request)](graph.md#getgraphstats).

### GodNode

Source: [graph.proto:232](https://github.com/egladman/magus/blob/main/proto/magus/graph/v1alpha1/graph.proto#L232).

| Field    | Type   | # | Description |
| -------- | ------ | - | ----------- |
| `id`     | string | 1 |             |
| `kind`   | string | 2 |             |
| `label`  | string | 3 |             |
| `degree` | int32  | 4 | in + out    |
| `in`     | int32  | 5 |             |
| `out`    | int32  | 6 |             |

Used by: [GetGraphStats (response)](graph.md#getgraphstats).

### GraphStats

Source: [graph.proto:221](https://github.com/egladman/magus/blob/main/proto/magus/graph/v1alpha1/graph.proto#L221).

| Field                    | Type                                 | # | Description |
| ------------------------ | ------------------------------------ | - | ----------- |
| `node_count`             | int32                                | 1 |             |
| `edge_count`             | int32                                | 2 |             |
| `gods`                   | [repeated GodNode](#godnode)         | 3 |             |
| `orphans`                | [repeated Orphan](#orphan)           | 4 |             |
| `coverage`               | [repeated DocCoverage](#doccoverage) | 5 |             |
| `isolated_count`         | int32                                | 6 |             |
| `component_count`        | int32                                | 7 |             |
| `largest_component_size` | int32                                | 8 |             |

Used by: [GetGraphStats (response)](graph.md#getgraphstats).

### Match

Match is one ranked node. staleness/outrun\_days carry the EVIDENCE for a prose match that ranked down because the thing it describes moved on without it, so the weight is never silent. Empty on anything not penalized.

Source: [graph.proto:127](https://github.com/egladman/magus/blob/main/proto/magus/graph/v1alpha1/graph.proto#L127).

| Field         | Type   | # | Description |
| ------------- | ------ | - | ----------- |
| `id`          | string | 1 |             |
| `kind`        | string | 2 |             |
| `label`       | string | 3 |             |
| `score`       | int32  | 4 |             |
| `staleness`   | string | 5 |             |
| `outrun_days` | int32  | 6 |             |

Used by: [QueryNodes (response)](graph.md#querynodes), [ResolveNodes (response)](graph.md#resolvenodes).

### Node

Node is one graph node.

Source: [graph.proto:29](https://github.com/egladman/magus/blob/main/proto/magus/graph/v1alpha1/graph.proto#L29).

| Field    | Type                | # | Description                                 |
| -------- | ------------------- | - | ------------------------------------------- |
| `id`     | string              | 1 |                                             |
| `kind`   | string              | 2 |                                             |
| `label`  | string              | 3 |                                             |
| `doc`    | string              | 4 |                                             |
| `source` | string              | 5 | path or path:line provenance                |
| `attrs`  | map<string, string> | 6 | kind-specific (charm pointer, MGS URL, ...) |

Used by: [ExplainNode (response)](graph.md#explainnode), [QueryNodes (response)](graph.md#querynodes).

### NodeContext

Source: [graph.proto:140](https://github.com/egladman/magus/blob/main/proto/magus/graph/v1alpha1/graph.proto#L140).

| Field          | Type                         | # | Description                                                                                                                                                                                                                                                                                                                                          |
| -------------- | ---------------------------- | - | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `node`         | [Node](#node)                | 1 |                                                                                                                                                                                                                                                                                                                                                      |
| `blast_radius` | int32                        | 2 | How many nodes transitively REACH this one, by ANY relation. A reach measure - read it as "how connected is this", not as "what breaks if I change it". Those diverge: nothing depends\_on a spell, so a spell scores in the hundreds here and has no dependents at all. FindDependents answers the rebuild question; do not substitute this for it. |
| `out`          | [repeated EdgeRef](#edgeref) | 3 |                                                                                                                                                                                                                                                                                                                                                      |
| `in`           | [repeated EdgeRef](#edgeref) | 4 |                                                                                                                                                                                                                                                                                                                                                      |

Used by: [ExplainNode (response)](graph.md#explainnode).

### Orphan

Orphan is a node missing the connection its KIND implies - a doc that documents nothing, a spell no target uses - with the reason in plain English. Not the same as "no edges at all": the reason is what makes it actionable.

Source: [graph.proto:244](https://github.com/egladman/magus/blob/main/proto/magus/graph/v1alpha1/graph.proto#L244).

| Field    | Type   | # | Description |
| -------- | ------ | - | ----------- |
| `id`     | string | 1 |             |
| `kind`   | string | 2 |             |
| `label`  | string | 3 |             |
| `reason` | string | 4 |             |

Used by: [GetGraphStats (response)](graph.md#getgraphstats).

### Path

Source: [graph.proto:201](https://github.com/egladman/magus/blob/main/proto/magus/graph/v1alpha1/graph.proto#L201).

| Field   | Type                           | # | Description |
| ------- | ------------------------------ | - | ----------- |
| `from`  | string                         | 1 |             |
| `to`    | string                         | 2 |             |
| `found` | bool                           | 3 |             |
| `steps` | [repeated PathStep](#pathstep) | 4 |             |

Used by: [FindPath (response)](graph.md#findpath).

### PathStep

PathStep is one hop as WALKED (from -> to). forward=false means the path traversed the underlying edge against its own direction.

Source: [graph.proto:210](https://github.com/egladman/magus/blob/main/proto/magus/graph/v1alpha1/graph.proto#L210).

| Field      | Type   | # | Description |
| ---------- | ------ | - | ----------- |
| `from`     | string | 1 |             |
| `to`       | string | 2 |             |
| `relation` | string | 3 |             |
| `forward`  | bool   | 4 |             |

Used by: [FindPath (response)](graph.md#findpath).

### QueryNodesRequest

Source: [graph.proto:97](https://github.com/egladman/magus/blob/main/proto/magus/graph/v1alpha1/graph.proto#L97).

| Field       | Type   | # | Description                                  |
| ----------- | ------ | - | -------------------------------------------- |
| `query`     | string | 1 | the magus query grammar, verbatim            |
| `budget`    | int32  | 2 | neighborhood node budget; 0 = server default |
| `offset`    | int32  | 3 |                                              |
| `page_size` | int32  | 4 | 0 = every match from offset on               |

Used by: [QueryNodes (request)](graph.md#querynodes).

### QueryNodesResponse

Source: [graph.proto:104](https://github.com/egladman/magus/blob/main/proto/magus/graph/v1alpha1/graph.proto#L104).

| Field         | Type                     | # | Description                  |
| ------------- | ------------------------ | - | ---------------------------- |
| `query`       | string                   | 1 |                              |
| `budget`      | int32                    | 2 |                              |
| `match_count` | int32                    | 3 | TOTAL matches, not this page |
| `offset`      | int32                    | 4 |                              |
| `matches`     | [repeated Match](#match) | 5 |                              |
| `nodes`       | [repeated Node](#node)   | 6 |                              |
| `links`       | [repeated Edge](#edge)   | 7 |                              |
| `answer`      | [Answer](#answer)        | 8 |                              |

Used by: [QueryNodes (response)](graph.md#querynodes).

### ResolveNodesRequest

Source: [graph.proto:115](https://github.com/egladman/magus/blob/main/proto/magus/graph/v1alpha1/graph.proto#L115).

| Field       | Type   | # | Description |
| ----------- | ------ | - | ----------- |
| `reference` | string | 1 |             |
| `limit`     | int32  | 2 |             |

Used by: [ResolveNodes (request)](graph.md#resolvenodes).

### ResolveNodesResponse

Source: [graph.proto:120](https://github.com/egladman/magus/blob/main/proto/magus/graph/v1alpha1/graph.proto#L120).

| Field     | Type                     | # | Description |
| --------- | ------------------------ | - | ----------- |
| `matches` | [repeated Match](#match) | 1 |             |

Used by: [ResolveNodes (response)](graph.md#resolvenodes).

### SymbolGap

SymbolGap is one project whose declared symbol index magus could not read: the evidence behind an unknown verdict. The project is flattened to its two wire fields rather than nested, because ProjectRef's third field is an absolute host path that never leaves the server.

Source: [graph.proto:274](https://github.com/egladman/magus/blob/main/proto/magus/graph/v1alpha1/graph.proto#L274).

| Field          | Type   | # | Description                                                   |
| -------------- | ------ | - | ------------------------------------------------------------- |
| `project_path` | string | 1 | workspace-relative; "." is the root                           |
| `project_name` | string | 2 | the human label, which differs from the path only at the root |
| `state`        | string | 3 |                                                               |
| `detail`       | string | 4 |                                                               |

Used by: [QueryNodes (response)](graph.md#querynodes).

## Enums

### EdgeDirection

Source: [graph.proto:161](https://github.com/egladman/magus/blob/main/proto/magus/graph/v1alpha1/graph.proto#L161).

| Value                        | # | Description                         |
| ---------------------------- | - | ----------------------------------- |
| `EDGE_DIRECTION_UNSPECIFIED` | 0 |                                     |
| `EDGE_DIRECTION_OUT`         | 1 | the focus node is the edge's source |
| `EDGE_DIRECTION_IN`          | 2 | the focus node is the edge's target |

Used by: [ExplainNode (response)](graph.md#explainnode).

