---
title: pipe module
generated_from: reference/buzz/
aliases: [modules/pipe]
description: Read the records a magus stage upstream in a pipe writes, and write records for the stage downstream.
tags: [pipe, module, stdlib, magusfile]
---

# pipe

Read the records a magus stage upstream in a pipe writes, and write records for the stage downstream.

> **Naming convention:** import the module under its bare name (`import "pipe"`), reach members with a backslash, and call methods in `camelCase`: `pipe\someMethod`.

<!-- -->

> [!NOTE]
> The examples below are reference-only. `pipe` performs real IO (filesystem, process, network, or environment access) that the in-browser playground's sandbox cannot provide, so it is not registered there and its examples have no Run button. Pure-compute modules such as `strings` and `json` run their examples live in the page.

## Methods

### more

Report whether another record is coming from the magus stage writing this script's stdin, waiting for it or for that stage to end. Errors when no magus stage writing records feeds this script.

**Signature:** `pipe\more() -> bool` - [source](https://github.com/egladman/magus/blob/main/std/pipe.go#L157)

**Returns:** bool

**Example:**

```buzz
import "std";
import "pipe";

// magus run test . | magus buzz failures.buzz
// more waits for the next record, so each failure prints as the run reports it.
try {
    while (pipe\more()) {
        final rec = pipe\next();
        if (rec.@"type" == "run.target.result" and rec.status == "failed") {
            std\print("FAIL {rec.project}:{rec.target}  magus query output {rec.ref}");
        }
    }
} catch (e) {
    std\print("no magus run is piped into this script");
}
```

### next

Return the next record from the magus stage writing this script's stdin, waiting for it. Errors past the last record, and when no magus stage writing records feeds this script.

**Signature:** `pipe\next() -> PipeRecord` - [source](https://github.com/egladman/magus/blob/main/std/pipe.go#L170)

**Returns:** map[string]any

### all

Return every record not yet read, once the magus stage writing this script's stdin has ended. Errors when no magus stage writing records feeds this script.

**Signature:** `pipe\all() -> [PipeRecord]` - [source](https://github.com/egladman/magus/blob/main/std/pipe.go#L186)

**Returns:** any

### emit

Write record to stdout for the stage downstream. A record read from upstream passes through byte for byte; one built here is written from its fields, and needs a type. A run.scope record's projects are what a run downstream that names none runs on.

**Signature:** `pipe\emit(record)` - [source](https://github.com/egladman/magus/blob/main/std/pipe.go#L207)

| Parameter | Type             | Optional | Description |
| --------- | ---------------- | -------- | ----------- |
| `record`  | `map[string]any` |          |             |

**Example:**

```buzz
import "std";
import "pipe";

// magus run test . | magus buzz retry.buzz | magus run test
// A run.scope record names the projects a run downstream runs on when it names none,
// so this passes on only the projects that failed.
try {
    var failed: mut [str] = mut [];
    foreach (rec in pipe\all()) {
        if (rec.@"type" == "run.target.result" and rec.status == "failed") {
            failed.append(rec.project);
        }
    }
    pipe\emit(pipe\PipeRecord{ @"type" = "run.scope", projects = failed });
} catch (e) {
    std\print("no magus run is piped into this script");
}
```

### outputs

Return the files the target of record declared as outputs and that exist on disk now, sorted by workspace-relative path. record names a project and a target, like a run.target.result.

**Signature:** `pipe\outputs(record) -> [Artifact]` - [source](https://github.com/egladman/magus/blob/main/std/pipe.go#L238)

| Parameter | Type             | Optional | Description |
| --------- | ---------------- | -------- | ----------- |
| `record`  | `map[string]any` |          |             |

**Returns:** any

**Example:**

```buzz
import "std";
import "pipe";

// magus run build . | magus buzz export.buzz
// Copies every file the build declared and produced under dist-copy/, keeping the tree.
try {
    foreach (rec in pipe\all()) {
        if (rec.@"type" == "run.target.result") {
            foreach (a in pipe\outputs(rec)) {
                std\print(pipe\exportTo(a, dest: "dist-copy/{a.path}"));
            }
        }
    }
} catch (e) {
    std\print("no magus run is piped into this script");
}
```

### exportTo

Copy artifact to dest, keeping its mode, and return dest. It writes a temporary file beside dest and renames it into place, so a symlink at dest is replaced rather than written through and an artifact exported onto itself survives.

**Signature:** `pipe\exportTo(artifact, dest) -> string` - [source](https://github.com/egladman/magus/blob/main/std/pipe.go#L281)

| Parameter  | Type             | Optional | Description |
| ---------- | ---------------- | -------- | ----------- |
| `artifact` | `map[string]any` |          |             |
| `dest`     | `string`         |          |             |

**Returns:** string

### history

Return every version of artifact the cache stored, newest first, with identical consecutive content collapsed: when its bytes changed, which its VCS history cannot say.

**Signature:** `pipe\history(artifact) -> [ArtifactVersion]` - [source](https://github.com/egladman/magus/blob/main/std/pipe.go#L346)

| Parameter  | Type             | Optional | Description |
| ---------- | ---------------- | -------- | ----------- |
| `artifact` | `map[string]any` |          |             |

**Returns:** any

### diff

Compare artifact on disk against its most recent different cached version with your difftool: $MAGUS_DIFFTOOL, else $DIFFTOOL, else `git diff --no-index`. It renders nothing itself.

**Signature:** `pipe\diff(artifact)` - [source](https://github.com/egladman/magus/blob/main/std/pipe.go#L371)

| Parameter  | Type             | Optional | Description |
| ---------- | ---------------- | -------- | ----------- |
| `artifact` | `map[string]any` |          |             |

### value

Return what a target returned, a str or a [str], from its run.target.value record. Errors for any other record.

**Signature:** `pipe\value(record) -> any` - [source](https://github.com/egladman/magus/blob/main/std/pipe.go#L456)

| Parameter | Type             | Optional | Description |
| --------- | ---------------- | -------- | ----------- |
| `record`  | `map[string]any` |          |             |

**Returns:** any

