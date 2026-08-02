---
title: Daemon API
description: "The magus daemon's Connect, gRPC, and gRPC-Web API: every service, method, message, and enum, generated from the .proto contract."
tags: [api, proto, protobuf, connect, grpc, daemon, reference]
---

# Daemon API

The daemon serves its API over [Connect](https://connectrpc.com), which speaks three protocols on one endpoint: Connect's own browser-native HTTP, gRPC, and gRPC-Web. Anything that can send an HTTP request can call it, so a generated client is optional.

This reference is generated from the `.proto` contract, so it cannot drift from what the daemon serves. The console is one consumer of this API and has no privileged access to it - a front end you write yourself can do everything the console does.

## Before you call anything

Start the daemon with `magus server start`. See [the console reference](../console.md) for the endpoint and port, and [the auth diagnostics](../codes/auth/) for what a rejected token means. Requests carry a hashed, expiring `mgs_` bearer token.

## Services

| Service | Methods | Package |
|---------|---------|--------|
| [ActivityService](activity.md) | 2 | `magus.activity.v1` |
| [JobService](job.md) | 5 | `magus.job.v1` |
| [MemoryService](memory.md) | 5 | `magus.memory.v1` |
| [MetricsService](metrics.md) | 2 | `magus.metrics.v1` |
| [StatusService](status.md) | 2 | `magus.status.v1` |
| [TokenService](token.md) | 2 | `magus.token.v1` |
| [ViewerService](viewer.md) | 3 | `magus.viewer.v1` |

## Calling a method without a generated client

A unary Connect method is a plain POST with a JSON body, so `curl` is a complete client:

```sh
curl -X POST \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer $MAGUS_TOKEN" \
  -d '{}' \
  http://127.0.0.1:7391/magus.activity.v1.ActivityService/ListActivity
```

The path is always `/<package>.<Service>/<Method>`, which every page below states per method.

