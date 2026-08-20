---
title: magus.query.v1alpha1
generated_from: reference/api/
description: Package magus.query.v1alpha1 holds only SHARED query PRIMITIVES - the building blocks each domain composes into its own typed query message.
tags: [api, proto, connect, grpc, query-v1alpha1]
---

# magus.query.v1alpha1

Package magus.query.v1alpha1 holds only SHARED query PRIMITIVES - the building blocks each domain composes into its own typed query message. There is deliberately NO generic Query{repeated Term} bag: log fields are not graph fields, so a lowest-common-denominator filter would be dishonest. The viewer composes an EventQuery from these primitives plus its own event fields; a future graph contract composes a GraphQuery from these plus its own.

Package `magus.query.v1alpha1`, defined in `proto/magus/query/v1alpha1/query.proto`. Source: [query.proto:8](https://github.com/egladman/magus/blob/main/proto/magus/query/v1alpha1/query.proto#L8). Declares no service of its own; part of the [daemon API](../../index.md).

## Messages

### StringMatch

StringMatch is one negatable string comparison against whatever field the composing message names it for. negate inverts the match (a leading "-" in the DSL). Matching is case-insensitive; multiple matches on one field AND together.

Source: [query.proto:15](https://github.com/egladman/magus/blob/main/proto/magus/query/v1alpha1/query.proto#L15).

| Field | Type | # | Description |
|-------|------|---|-------------|
| `value` | string | 1 |  |
| `negate` | bool | 2 |  |

Used by: [ListEvents (request)](../../viewer/v1alpha1/viewer.md#listevents), [StreamEvents (request)](../../viewer/v1alpha1/viewer.md#streamevents).

### TimeRange

TimeRange bounds a query to items between since and until (inclusive); either bound may be unset for an open-ended range. since doubles as a live-stream resume cursor.

Source: [query.proto:22](https://github.com/egladman/magus/blob/main/proto/magus/query/v1alpha1/query.proto#L22).

| Field | Type | # | Description |
|-------|------|---|-------------|
| `since` | Timestamp | 1 |  |
| `until` | Timestamp | 2 |  |

Used by: [ListActivityEvents (request)](../../activity/v1alpha1/activity.md#listactivityevents), [ListEvents (request)](../../viewer/v1alpha1/viewer.md#listevents), [StreamEvents (request)](../../viewer/v1alpha1/viewer.md#streamevents).

