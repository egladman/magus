---
title: ToolService
generated_from: reference/api/
description: ToolService serves the toolchain view.
tags: [api, proto, connect, grpc, toolservice]
---

# ToolService

ToolService serves the toolchain view. Read-only: nothing here installs, selects, or moves a version.

Package `magus.tool.v1alpha1`, defined in `proto/magus/tool/v1alpha1/tool.proto`. Source: [tool.proto:90](https://github.com/egladman/magus/blob/main/proto/magus/tool/v1alpha1/tool.proto#L90). Part of the [daemon API](../../index.md).

## Methods

### ListTools

ListTools returns every project's tools with their windows and verdicts.

The name does not match the repeated field (projects), which AIP-132 would have it do. Left deliberately: this returns a two-level view, projects each carrying their tools, because a tool version is a per-project fact and the dashboard renders it grouped that way. Flattening to `repeated Tool` to satisfy the rule would make every Tool carry its own project path and lose the grouping; renaming this ListProjects would leave the toolchain service with no RPC that mentions a tool. AIP-132 governs a collection of one resource, and this is not one.

`POST /magus.tool.v1alpha1.ToolService/ListTools`: unary. Source: [tool.proto:100](https://github.com/egladman/magus/blob/main/proto/magus/tool/v1alpha1/tool.proto#L100).

Takes [ListToolsRequest](#listtoolsrequest), returns [ListToolsResponse](#listtoolsresponse).

## Messages

### ListToolsRequest

Source: [tool.proto:103](https://github.com/egladman/magus/blob/main/proto/magus/tool/v1alpha1/tool.proto#L103).

| Field | Type | # | Description |
|-------|------|---|-------------|
| `parent` | string | 1 | The project whose tools these are, as a workspace-relative path; empty returns every project. Named parent per AIP-132 - a project is a real container here, unlike the workspace-scoped collections in this API, which have no second value to hold. |

Used by: [ListTools (request)](tool.md#listtools).

### ListToolsResponse

Source: [tool.proto:110](https://github.com/egladman/magus/blob/main/proto/magus/tool/v1alpha1/tool.proto#L110).

| Field | Type | # | Description |
|-------|------|---|-------------|
| `projects` | [repeated Project](#project) | 1 |  |

Used by: [ListTools (response)](tool.md#listtools).

### Project

Project groups the tools one project drives, since a window is declared per project and the same binary can be held to different bounds in different projects.

Source: [tool.proto:82](https://github.com/egladman/magus/blob/main/proto/magus/tool/v1alpha1/tool.proto#L82).

| Field | Type | # | Description |
|-------|------|---|-------------|
| `path` | string | 1 | workspace-relative, "." for the root |
| `name` | string | 2 |  |
| `tools` | [repeated Tool](#tool) | 3 |  |

Used by: [ListTools (response)](tool.md#listtools).

### Tool

Tool is one binary a spell drives, as this workspace currently sees it.

Source: [tool.proto:45](https://github.com/egladman/magus/blob/main/proto/magus/tool/v1alpha1/tool.proto#L45).

| Field | Type | # | Description |
|-------|------|---|-------------|
| `bin` | string | 1 | the binary name, e.g. "go", "node" |
| `spell` | string | 2 | the spell that declares it, e.g. "go", "typescript" |
| `installed_version` | string | 3 | installed\_version is what the probe extracted, canonical "vX.Y.Z". Empty when the tool is absent or printed nothing version-shaped; that is not a violation, so it pairs with VERDICT\_UNKNOWN rather than a failure. |
| `spell_bounds` | [VersionBounds](#versionbounds) | 4 | The two declarations, kept SEPARATE rather than pre-merged, because the first question a reader has about a failing bound is who set it. The CLI diagnostic cannot say (Intersect discards provenance); a table has room to. what the spell's ops need to function |
| `workspace_bounds` | [VersionBounds](#versionbounds) | 5 | what this project declared in its magusfile |
| `effective` | [VersionBounds](#versionbounds) | 6 | the intersection actually enforced, narrower wins |
| `verdict` | [Verdict](#verdict) | 7 |  |
| `diagnostic_code` | string | 8 | "MGS3005"/"MGS3006" when violated, else empty |
| `probe_time` | Timestamp | 9 | probe\_time is when this version was read. A console page has no build to piggyback on, so the probe behind it may be older than the page; surfacing the age is honest where implying live is not. |

_Reserved: 10; `enforced`._

Used by: [ListTools (response)](tool.md#listtools).

### VersionBounds

VersionBounds is a version window: an inclusive floor and an exclusive ceiling, each a plain version. Mirrors spells.VersionBounds. Both empty means unconstrained.

below is the first version REJECTED, not the last accepted, so a UI must not render it as "max": below "25" accepts 24.19.0 and rejects 25.0.0.

Source: [tool.proto:39](https://github.com/egladman/magus/blob/main/proto/magus/tool/v1alpha1/tool.proto#L39).

| Field | Type | # | Description |
|-------|------|---|-------------|
| `min` | string | 1 |  |
| `below` | string | 2 |  |

Used by: [ListTools (response)](tool.md#listtools).

## Enums

### Verdict

Verdict is how a probed version sits in its window. Mirrors spells.Verdict.

Source: [tool.proto:23](https://github.com/egladman/magus/blob/main/proto/magus/tool/v1alpha1/tool.proto#L23).

| Value | # | Description |
|-------|---|-------------|
| `VERDICT_UNSPECIFIED` | 0 |  |
| `VERDICT_INSIDE` | 1 | satisfies every declared bound |
| `VERDICT_TOO_OLD` | 2 | below min; the CLI raises MGS3005 |
| `VERDICT_TOO_NEW` | 3 | at or above below; the CLI raises MGS3006 |
| `VERDICT_UNKNOWN` | 4 | VERDICT\_UNKNOWN means magus could not make the comparison: the probe failed, its output carried no version, or a bound is unparsable. Never a violation, and deliberately distinct from INSIDE - "we could not check" must not render as "fine". |

Used by: [ListTools (response)](tool.md#listtools).

