---
title: Daemon API
generated_from: proto/magus/**/*.proto
description: "The magus daemon's Connect, gRPC, and gRPC-Web API: every service, method, message, and enum, generated from the .proto contract."
tags: [api, proto, protobuf, connect, grpc, daemon, reference]
---

# Daemon API

The daemon serves its API over [Connect](https://connectrpc.com), which speaks three protocols on one endpoint: Connect's own browser-native HTTP, gRPC, and gRPC-Web. Anything that can send an HTTP request can call it, so a generated client is optional.

This reference is generated from the `.proto` contract, so it cannot drift from what the daemon serves. The Console is a reference frontend and has no privileged access to the API. A frontend you build can use the same published contract. Every service, method, message, and enum heading below links to the exact line in the `.proto` source that defines it.

## Before you call anything

Start the daemon with `magus server start`. See [the console reference](../console.md) for the endpoint and port, and [the auth diagnostics](../codes/auth/) for what a rejected token means. Requests carry a hashed, expiring `mgs_` bearer token.

## Services

| Service | Methods | Package |
|---------|---------|--------|
| [ActivityService](activity/v1alpha1/activity.md) | 2 | `magus.activity.v1alpha1` |
| [GraphService](graph/v1alpha1/graph.md) | 7 | `magus.graph.v1alpha1` |
| [InsightService](insight/v1alpha1/insight.md) | 1 | `magus.insight.v1alpha1` |
| [JobService](job/v1alpha1/job.md) | 2 | `magus.job.v1alpha1` |
| [MemoryService](memory/v1alpha1/memory.md) | 5 | `magus.memory.v1alpha1` |
| [MetricsService](metrics/v1alpha1/metrics.md) | 2 | `magus.metrics.v1alpha1` |
| [NotesService](notes/v1alpha1/notes.md) | 2 | `magus.notes.v1alpha1` |
| [StatusService](status/v1alpha1/status.md) | 2 | `magus.status.v1alpha1` |
| [TokenService](token/v1alpha1/token.md) | 3 | `magus.token.v1alpha1` |
| [ToolService](tool/v1alpha1/tool.md) | 1 | `magus.tool.v1alpha1` |
| [ViewerService](viewer/v1alpha1/viewer.md) | 3 | `magus.viewer.v1alpha1` |

## Shared types

A package with no service of its own: its types are documented here instead of on a service page, and a service that uses one links to it.

| Package | File |
|---------|------|
| [magus.query.v1alpha1](query/v1alpha1/query.md) | `proto/magus/query/v1alpha1/query.proto` |

## Calling a method without a generated client

A unary Connect method is a plain POST with a JSON body, so `curl` is a complete client. protojson, not the `.proto` field name, decides the wire shape:

- Field names are lowerCamelCase (`page_size` in the tables below is `pageSize` on the wire).
- `int64`, `uint64`, `fixed64`, and `sfixed64` values are JSON strings, not numbers (large values overflow a JSON number's safe integer range).
- `bytes` is base64. A `Timestamp` is an RFC 3339 string; a `Duration` is a string like `"1.5s"`.
- An enum serializes as its value name (`"TOKEN_SCOPE_CONNECTOR"`), not its number.

```sh
curl -X POST \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer $MAGUS_TOKEN" \
  -d '{"pageSize":0,"pageToken":""}' \
  http://127.0.0.1:7391/magus.activity.v1alpha1.ActivityService/ListActivityEvents
```

The path is always `/<package>.<Service>/<Method>`, which every page below states per method. A request body shown as `{}` takes no fields; a longer one is a shallow skeleton (top-level scalar and enum fields only) and does not attempt to satisfy every field's constraints - a `string.pattern` rule still needs a value matching that pattern.

## Streaming methods

A server-streaming method returns a sequence of messages over one HTTP response rather than one body, so a single `curl -d` request cannot cleanly demux it. Use a generated Connect client, or a tool built for streaming RPCs such as [grpcurl](https://github.com/fullstorydev/grpcurl) (`grpcurl -plaintext ... /magus.viewer.v1.ViewerService/StreamEvents`). Streaming methods:

- [MetricsService.StreamMetrics](metrics/v1alpha1/metrics.md#streammetrics) (server streaming)
- [StatusService.StreamStatus](status/v1alpha1/status.md#streamstatus) (server streaming)
- [ViewerService.StreamEvents](viewer/v1alpha1/viewer.md#streamevents) (server streaming)

