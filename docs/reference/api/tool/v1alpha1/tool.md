---
title: ToolService
generated_from: reference/api/
description: ToolService serves the toolchain view.
tags: [api, proto, connect, grpc, toolservice]
---

# ToolService

ToolService serves the toolchain view. Read-only: nothing here installs, selects, or moves a version. ListTools reaches the network through the lifecycle provider; the server memoizes that answer, and Lifecycle names what it read.

Package `magus.tool.v1alpha1`, defined in `proto/magus/tool/v1alpha1/tool.proto`. Source: [tool.proto:146](https://github.com/egladman/magus/blob/main/proto/magus/tool/v1alpha1/tool.proto#L146). Part of the [daemon API](../../index.md).

## Methods

### ListTools

ListTools returns every project's tools with their windows and verdicts.

The name does not match the repeated field (projects), which AIP-132 would have it do. Left deliberately: this returns a two-level view, projects each carrying their tools, because a tool version is a per-project fact and the dashboard renders it grouped that way. Flattening to `repeated Tool` to satisfy the rule would make every Tool carry its own project path and lose the grouping; renaming this ListProjects would leave the toolchain service with no RPC that mentions a tool. AIP-132 governs a collection of one resource, and this is not one.

`POST /magus.tool.v1alpha1.ToolService/ListTools`: unary. Source: [tool.proto:156](https://github.com/egladman/magus/blob/main/proto/magus/tool/v1alpha1/tool.proto#L156).

Takes [ListToolsRequest](#listtoolsrequest), returns [ListToolsResponse](#listtoolsresponse).

## Messages

### Lifecycle

Lifecycle says where every Tool's cycle, eol and support came from.

Source: [tool.proto:57](https://github.com/egladman/magus/blob/main/proto/magus/tool/v1alpha1/tool.proto#L57).

| Field        | Type                              | # | Description                                                                                                                                     |
| ------------ | --------------------------------- | - | ----------------------------------------------------------------------------------------------------------------------------------------------- |
| `provider`   | string                            | 1 | the spell wired as the lifecycle provider, empty when none is                                                                                   |
| `state`      | [LifecycleState](#lifecyclestate) | 2 |                                                                                                                                                 |
| `sources`    | repeated string                   | 3 | the URLs the provider read                                                                                                                      |
| `as_of`      | Timestamp                         | 4 | as\_of is the OLDEST upstream last-modified among the answers: the data is no fresher than its stalest source. Unset when nothing was answered. |
| `fetched_at` | Timestamp                         | 5 | fetched\_at is when the provider was asked; on a replay, when the replayed answer was.                                                          |
| `detail`     | string                            | 6 | why the provider was not asked or did not answer                                                                                                |

Used by: [ListTools (response)](tool.md#listtools).

### ListToolsRequest

Source: [tool.proto:159](https://github.com/egladman/magus/blob/main/proto/magus/tool/v1alpha1/tool.proto#L159).

| Field    | Type   | # | Description                                                                                                                                                                                                                                           |
| -------- | ------ | - | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `parent` | string | 1 | The project whose tools these are, as a workspace-relative path; empty returns every project. Named parent per AIP-132 - a project is a real container here, unlike the workspace-scoped collections in this API, which have no second value to hold. |

Used by: [ListTools (request)](tool.md#listtools).

### ListToolsResponse

Source: [tool.proto:166](https://github.com/egladman/magus/blob/main/proto/magus/tool/v1alpha1/tool.proto#L166).

| Field       | Type                         | # | Description |
| ----------- | ---------------------------- | - | ----------- |
| `projects`  | [repeated Project](#project) | 1 |             |
| `lifecycle` | [Lifecycle](#lifecycle)      | 2 |             |

Used by: [ListTools (response)](tool.md#listtools).

### Project

Project groups the tools one project drives, since a window is declared per project and the same binary can be held to different bounds in different projects.

Source: [tool.proto:137](https://github.com/egladman/magus/blob/main/proto/magus/tool/v1alpha1/tool.proto#L137).

| Field   | Type                   | # | Description                          |
| ------- | ---------------------- | - | ------------------------------------ |
| `path`  | string                 | 1 | workspace-relative, "." for the root |
| `name`  | string                 | 2 |                                      |
| `tools` | [repeated Tool](#tool) | 3 |                                      |

Used by: [ListTools (response)](tool.md#listtools).

### Tool

Tool is one binary a spell drives, as this workspace currently sees it.

Source: [tool.proto:80](https://github.com/egladman/magus/blob/main/proto/magus/tool/v1alpha1/tool.proto#L80).

| Field               | Type                            | #  | Description                                                                                                                                                                                                                                                        |
| ------------------- | ------------------------------- | -- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| `bin`               | string                          | 1  | the binary name, e.g. "go", "node"                                                                                                                                                                                                                                 |
| `spell`             | string                          | 2  | the spell that declares it, e.g. "go", "typescript"                                                                                                                                                                                                                |
| `installed_version` | string                          | 3  | installed\_version is what the probe extracted, canonical "vX.Y.Z". Empty when the tool is absent or printed nothing version-shaped; that is not a violation, so it pairs with VERDICT\_UNKNOWN rather than a failure.                                             |
| `spell_bounds`      | [VersionBounds](#versionbounds) | 4  | The two declarations, kept SEPARATE rather than pre-merged, because the first question a reader has about a failing bound is who set it. The CLI diagnostic cannot say (Intersect discards provenance); a table has room to. what the spell's ops need to function |
| `workspace_bounds`  | [VersionBounds](#versionbounds) | 5  | what this project declared in its magusfile                                                                                                                                                                                                                        |
| `effective`         | [VersionBounds](#versionbounds) | 6  | the intersection actually enforced, narrower wins                                                                                                                                                                                                                  |
| `verdict`           | [Verdict](#verdict)             | 7  |                                                                                                                                                                                                                                                                    |
| `diagnostic_code`   | string                          | 8  | "MGS3005"/"MGS3006" when violated, else empty                                                                                                                                                                                                                      |
| `probe_time`        | Timestamp                       | 9  | probe\_time is when this version was read. A console page has no build to piggyback on, so the probe behind it may be older than the page; surfacing the age is honest where implying live is not.                                                                 |
| `lifecycle`         | string                          | 11 | lifecycle is the product the spell names for this binary (spells.Tool.lifecycle), empty when it names none. cycle is the release line the installed version belongs to and eol that line's end date, both empty when nothing matched.                              |
| `cycle`             | string                          | 12 |                                                                                                                                                                                                                                                                    |
| `eol`               | string                          | 13 |                                                                                                                                                                                                                                                                    |
| `support`           | [Support](#support)             | 14 |                                                                                                                                                                                                                                                                    |
| `violation`         | bool                            | 15 | violation is true when the version sits outside its effective window (TOO\_OLD or TOO\_NEW), the rows the CLI raises MGS3005/MGS3006 for. UNKNOWN is never a violation. A client counts these rather than re-deriving the rule from verdict or diagnostic\_code.   |
| `spell_window`      | string                          | 16 | The three windows rendered the way `magus describe tools` prints them: ">= 22, < 25". Empty when that window constrains nothing. Clients show these as given; below is the first version rejected, so it renders as "< x", never as a max.                         |
| `workspace_window`  | string                          | 17 |                                                                                                                                                                                                                                                                    |
| `effective_window`  | string                          | 18 |                                                                                                                                                                                                                                                                    |

_Reserved: 10; `enforced`._

Used by: [ListTools (response)](tool.md#listtools).

### VersionBounds

VersionBounds is a version window: an inclusive floor and an exclusive ceiling, each a plain version. Mirrors spells.VersionBounds. Both empty means unconstrained.

below is the first version REJECTED, not the last accepted, so a UI must not render it as "max": below "25" accepts 24.19.0 and rejects 25.0.0.

Source: [tool.proto:74](https://github.com/egladman/magus/blob/main/proto/magus/tool/v1alpha1/tool.proto#L74).

| Field   | Type   | # | Description |
| ------- | ------ | - | ----------- |
| `min`   | string | 1 |             |
| `below` | string | 2 |             |

Used by: [ListTools (response)](tool.md#listtools).

## Enums

### LifecycleState

LifecycleState is where the end-of-life data came from. Mirrors the types.Lifecycle* states `magus describe tools -o json` prints.

Source: [tool.proto:47](https://github.com/egladman/magus/blob/main/proto/magus/tool/v1alpha1/tool.proto#L47).

| Value                         | # | Description                                             |
| ----------------------------- | - | ------------------------------------------------------- |
| `LIFECYCLE_STATE_UNSPECIFIED` | 0 |                                                         |
| `LIFECYCLE_STATE_LIVE`        | 1 | asked the provider for this response                    |
| `LIFECYCLE_STATE_CACHED`      | 2 | replayed a stored answer without asking                 |
| `LIFECYCLE_STATE_OFFLINE`     | 3 | MAGUS\_OFFLINE forbade asking; a stored answer, or none |
| `LIFECYCLE_STATE_UNREACHED`   | 4 | asked and not answered; a stored answer, or none        |
| `LIFECYCLE_STATE_UNWIRED`     | 5 | the workspace wires no lifecycle provider               |

Used by: [ListTools (response)](tool.md#listtools).

### Support

Support is where the installed version's release cycle stands. Mirrors spells.Support.

Source: [tool.proto:34](https://github.com/egladman/magus/blob/main/proto/magus/tool/v1alpha1/tool.proto#L34).

| Value                 | # | Description                                                                                                                                                                                              |
| --------------------- | - | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `SUPPORT_UNSPECIFIED` | 0 | the spell names no lifecycle product, or no provider is wired                                                                                                                                            |
| `SUPPORT_SUPPORTED`   | 1 | before its end-of-life date                                                                                                                                                                              |
| `SUPPORT_EOL`         | 2 | on or after its end-of-life date                                                                                                                                                                         |
| `SUPPORT_UNANNOUNCED` | 3 | upstream has named no end date                                                                                                                                                                           |
| `SUPPORT_UNKNOWN`     | 4 | SUPPORT\_UNKNOWN means the provider gave nothing to place the version with: it did not answer, it does not know the product, or no cycle carries the version. The response's lifecycle state says which. |

Used by: [ListTools (response)](tool.md#listtools).

### Verdict

Verdict is how a probed version sits in its window. Mirrors spells.Verdict.

Source: [tool.proto:22](https://github.com/egladman/magus/blob/main/proto/magus/tool/v1alpha1/tool.proto#L22).

| Value                 | # | Description                                                                                                                                                                                                                                       |
| --------------------- | - | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `VERDICT_UNSPECIFIED` | 0 |                                                                                                                                                                                                                                                   |
| `VERDICT_INSIDE`      | 1 | satisfies every declared bound                                                                                                                                                                                                                    |
| `VERDICT_TOO_OLD`     | 2 | below min; the CLI raises MGS3005                                                                                                                                                                                                                 |
| `VERDICT_TOO_NEW`     | 3 | at or above below; the CLI raises MGS3006                                                                                                                                                                                                         |
| `VERDICT_UNKNOWN`     | 4 | VERDICT\_UNKNOWN means magus could not make the comparison: the probe failed, its output carried no version, or a bound is unparsable. Never a violation, and deliberately distinct from INSIDE - "we could not check" must not render as "fine". |

Used by: [ListTools (response)](tool.md#listtools).

