---
title: magus.graph.v1alpha1
generated_from: reference/api/
description: "Package magus.graph.v1alpha1 is the versioned wire contract for the knowledge-graph output the daemon serves to the browser Graph Explorer (/api/v1/graph): an induced subgraph of nodes and edges plus its shape metadata."
tags: [api, proto, connect, grpc, graph-v1alpha1]
---

# magus.graph.v1alpha1

Package magus.graph.v1alpha1 is the versioned wire contract for the knowledge-graph output the daemon serves to the browser Graph Explorer (/api/v1/graph): an induced subgraph of nodes and edges plus its shape metadata. It mirrors types.KnowledgeGraphOutput; field names match that type's JSON so a protojson encoding is wire-compatible with the JSON the page already consumes. A sibling of magus.viewer.v1alpha1 and magus.status.v1alpha1. buf-breaking gates this file.

Package `magus.graph.v1alpha1`, defined in `proto/magus/graph/v1alpha1/graph.proto`. Source: [graph.proto:8](https://github.com/egladman/magus/blob/main/proto/magus/graph/v1alpha1/graph.proto#L8). Declares no service of its own; part of the [daemon API](../../index.md).

## Messages

### Edge

Edge is one directed graph edge (a "link").

Source: [graph.proto:38](https://github.com/egladman/magus/blob/main/proto/magus/graph/v1alpha1/graph.proto#L38).

| Field | Type | # | Description |
|-------|------|---|-------------|
| `source` | string | 1 |  |
| `target` | string | 2 |  |
| `relation` | string | 3 |  |
| `confidence` | string | 4 |  |
| `score` | double | 5 |  |
| `provenance` | string | 6 |  |

### Graph

Graph is a knowledge-graph projection: nodes, edges (links), and shape metadata.

Source: [graph.proto:11](https://github.com/egladman/magus/blob/main/proto/magus/graph/v1alpha1/graph.proto#L11).

| Field | Type | # | Description |
|-------|------|---|-------------|
| `definition` | string | 1 |  |
| `schema_version` | int32 | 2 |  |
| `directed` | bool | 3 |  |
| `multigraph` | bool | 4 |  |
| `node_count` | int32 | 5 |  |
| `edge_count` | int32 | 6 |  |
| `source_base` | string | 7 | source\_base is the workspace repo blob base URL (e.g. "<https://github.com/owner/repo/blob/main>"), so a viewer can turn a node's relative source path into a link. Empty when there is no remote / the forge is unrecognized. Additive; omitted when empty, so it never bumps the schema version. |
| `nodes` | [repeated Node](#node) | 8 |  |
| `links` | [repeated Edge](#edge) | 9 |  |

### Node

Node is one graph node.

Source: [graph.proto:28](https://github.com/egladman/magus/blob/main/proto/magus/graph/v1alpha1/graph.proto#L28).

| Field | Type | # | Description |
|-------|------|---|-------------|
| `id` | string | 1 |  |
| `kind` | string | 2 |  |
| `label` | string | 3 |  |
| `doc` | string | 4 |  |
| `source` | string | 5 | path or path:line provenance |
| `attrs` | map<string, string> | 6 | kind-specific (charm pointer, MGS URL, ...) |

