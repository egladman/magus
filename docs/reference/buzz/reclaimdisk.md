---
title: reclaimdisk module
generated_from: reference/buzz/
aliases: [modules/reclaimdisk]
description: "Plan and take back the disk a repository's git worktrees and merged agent branches hold, never a dirty, unpushed, leased, busy or locked worktree; tick is the one decider every ..."
tags: [reclaimdisk, module, stdlib, magusfile]
---

# reclaimdisk

Plan and take back the disk a repository's git worktrees and merged agent branches hold, never a dirty, unpushed, leased, busy or locked worktree; tick is the one decider every disk trigger calls, and trims only what a spell gives a budget.

> **Naming convention:** import the module under its bare name (`import "reclaimdisk"`), reach members with a backslash, and call methods in `camelCase`: `reclaimdisk\someMethod`.

## Methods

### parseWorktrees

parseWorktrees reads `git worktree list --porcelain`: one stanza per worktree, the main

**Signature:** `reclaimdisk\parseWorktrees(porcelain) -> [Worktree]`

| Parameter   | Type  | Optional | Description |
| ----------- | ----- | -------- | ----------- |
| `porcelain` | `str` |          |             |

**Returns:** [Worktree]

### displayPath

displayPath is path relative to the main worktree when it sits under it.

**Signature:** `reclaimdisk\displayPath(path, main) -> str`

| Parameter | Type  | Optional | Description |
| --------- | ----- | -------- | ----------- |
| `path`    | `str` |          |             |
| `main`    | `str` |          |             |

**Returns:** str

### age

**Signature:** `reclaimdisk\age(ms) -> str`

| Parameter | Type     | Optional | Description |
| --------- | -------- | -------- | ----------- |
| `ms`      | `double` |          |             |

**Returns:** str

### size

**Signature:** `reclaimdisk\size(kib) -> str`

| Parameter | Type  | Optional | Description |
| --------- | ----- | -------- | ----------- |
| `kib`     | `int` |          |             |

**Returns:** str

### parseCwds

**Signature:** `reclaimdisk\parseCwds(lsof) -> [Cwd]`

| Parameter | Type  | Optional | Description |
| --------- | ----- | -------- | ----------- |
| `lsof`    | `str` |          |             |

**Returns:** [Cwd]

### pidsByWorktree

pidsByWorktree gives each process to the deepest worktree holding its working directory:

**Signature:** `reclaimdisk\pidsByWorktree(cwds, paths) -> {str:[str]}`

| Parameter | Type    | Optional | Description |
| --------- | ------- | -------- | ----------- |
| `cwds`    | `[Cwd]` |          |             |
| `paths`   | `[str]` |          |             |

**Returns:** {str:[str]}

### liveLeases

liveLeases is each checkout's live jobs as "<id> (<state>)". Exited counts as live: the

**Signature:** `reclaimdisk\liveLeases(leases) -> {str:[str]}`

| Parameter | Type      | Optional | Description |
| --------- | --------- | -------- | ----------- |
| `leases`  | `[Lease]` |          |             |

**Returns:** {str:[str]}

### classify

classify decides one worktree, listing every reason to keep it rather than the first.

**Signature:** `reclaimdisk\classify(o, main, own, leases, pids, now, keepRecent, shown) -> Item`

| Parameter    | Type          | Optional | Description |
| ------------ | ------------- | -------- | ----------- |
| `o`          | `Observed`    |          |             |
| `main`       | `str`         |          |             |
| `own`        | `str`         |          |             |
| `leases`     | `{str:[str]}` |          |             |
| `pids`       | `{str:[str]}` |          |             |
| `now`        | `double`      |          |             |
| `keepRecent` | `double`      |          |             |
| `shown`      | `str`         |          |             |

**Returns:** Item

### branchItems

branchItems lists the agent branches whose loss loses nothing: no kept worktree holds

**Signature:** `reclaimdisk\branchItems(tips, held, removable, unique) -> [Item]`

| Parameter   | Type        | Optional | Description |
| ----------- | ----------- | -------- | ----------- |
| `tips`      | `{str:str}` |          |             |
| `held`      | `{str:str}` |          |             |
| `removable` | `[str]`     |          |             |
| `unique`    | `[str]`     |          |             |

**Returns:** [Item]

### parseUniqueBranches

parseUniqueBranches reads `git log --format=%D --decorate-refs=refs/heads/`: the names

**Signature:** `reclaimdisk\parseUniqueBranches(log) -> [str]`

| Parameter | Type  | Optional | Description |
| --------- | ----- | -------- | ----------- |
| `log`     | `str` |          |             |

**Returns:** [str]

### rank

rank orders the plan largest reclaim first; unmeasured rows follow, removable before kept.

**Signature:** `reclaimdisk\rank(items) -> [Item]`

| Parameter | Type     | Optional | Description |
| --------- | -------- | -------- | ----------- |
| `items`   | `[Item]` |          |             |

**Returns:** [Item]

### pickChunk

pickChunk is one chunk of the plan: the n removable worktrees untouched longest, the

**Signature:** `reclaimdisk\pickChunk(items, n) -> [Item]`

| Parameter | Type     | Optional | Description |
| --------- | -------- | -------- | ----------- |
| `items`   | `[Item]` |          |             |
| `n`       | `int`    |          |             |

**Returns:** [Item]

### totals

**Signature:** `reclaimdisk\totals(items) -> Totals`

| Parameter | Type     | Optional | Description |
| --------- | -------- | -------- | ----------- |
| `items`   | `[Item]` |          |             |

**Returns:** Totals

### render

**Signature:** `reclaimdisk\render(items) -> [str]`

| Parameter | Type     | Optional | Description |
| --------- | -------- | -------- | ----------- |
| `items`   | `[Item]` |          |             |

**Returns:** [str]

### guardLine

guardLine is the shell line the guard judges for one removal: the same argv vcs\cmd runs,

**Signature:** `reclaimdisk\guardLine(root, path) -> str?`

| Parameter | Type  | Optional | Description |
| --------- | ----- | -------- | ----------- |
| `root`    | `str` |          |             |
| `path`    | `str` |          |             |

**Returns:** str?

### headline

headline is the plan's one-line summary; trimmed, when not empty, is what a cache trim

**Signature:** `reclaimdisk\headline(items, applied, trimmed) -> str`

| Parameter | Type     | Optional | Description |
| --------- | -------- | -------- | ----------- |
| `items`   | `[Item]` |          |             |
| `applied` | `bool`   |          |             |
| `trimmed` | `str`    |          |             |

**Returns:** str

### timeLeft

timeLeft is the timeoutMs a call made now may take: 0, no bound, when deadline is 0, and

**Signature:** `reclaimdisk\timeLeft(deadline, now) -> double`

| Parameter  | Type     | Optional | Description |
| ---------- | -------- | -------- | ----------- |
| `deadline` | `double` |          |             |
| `now`      | `double` |          |             |

**Returns:** double

### status

status reads one worktree's dirt; a directory that is gone reads dirty -1.

**Signature:** `reclaimdisk\status(path, deadline) -> Status`

| Parameter  | Type     | Optional | Description |
| ---------- | -------- | -------- | ----------- |
| `path`     | `str`    |          |             |
| `deadline` | `double` |          |             |

**Returns:** Status

### survey

survey reads every worktree and agent branch of the repository at the working directory

**Signature:** `reclaimdisk\survey(base, now, keepRecent, measure, deadline) -> Survey`

| Parameter    | Type     | Optional | Description |
| ------------ | -------- | -------- | ----------- |
| `base`       | `str`    |          |             |
| `now`        | `double` |          |             |
| `keepRecent` | `double` |          |             |
| `measure`    | `bool`   |          |             |
| `deadline`   | `double` |          |             |

**Returns:** Survey

### apply

apply removes the removable rows and returns the plan with each row's result. A worktree

**Signature:** `reclaimdisk\apply(items, root, deadline) -> [Item]`

| Parameter  | Type     | Optional | Description |
| ---------- | -------- | -------- | ----------- |
| `items`    | `[Item]` |          |             |
| `root`     | `str`    |          |             |
| `deadline` | `double` |          |             |

**Returns:** [Item]

### trimCache

trimCache trims one cache to its budget, keeping files inside fence.

**Signature:** `reclaimdisk\trimCache(name, dir, trimMb, fence, deadline) -> CacheTrim`

| Parameter  | Type     | Optional | Description |
| ---------- | -------- | -------- | ----------- |
| `name`     | `str`    |          |             |
| `dir`      | `str`    |          |             |
| `trimMb`   | `int`    |          |             |
| `fence`    | `double` |          |             |
| `deadline` | `double` |          |             |

**Returns:** CacheTrim

### trimLine

trimLine says what one trim really did, null when it did nothing to say.

**Signature:** `reclaimdisk\trimLine(t, fence) -> str?`

| Parameter | Type        | Optional | Description |
| --------- | ----------- | -------- | ----------- |
| `t`       | `CacheTrim` |          |             |
| `fence`   | `double`    |          |             |

**Returns:** str?

### trimDeclared

trimDeclared trims every cache a spell gives a budget and returns each

**Signature:** `reclaimdisk\trimDeclared(fence, deadline) -> [CacheTrim]`

| Parameter  | Type     | Optional | Description |
| ---------- | -------- | -------- | ----------- |
| `fence`    | `double` |          |             |
| `deadline` | `double` |          |             |

**Returns:** [CacheTrim]

### chunk

chunk is the worktree half of a reclaim: the CHUNK_WORKTREES removable

**Signature:** `reclaimdisk\chunk(deadline) -> [Item]`

| Parameter  | Type     | Optional | Description |
| ---------- | -------- | -------- | ----------- |
| `deadline` | `double` |          |             |

**Returns:** [Item]

### keptLine

keptLine counts what a reclaim leaves for a person, "" when it leaves none.

**Signature:** `reclaimdisk\keptLine(items) -> str`

| Parameter | Type     | Optional | Description |
| --------- | -------- | -------- | ----------- |
| `items`   | `[Item]` |          |             |

**Returns:** str

### tick

tick is the one decider every trigger calls. It samples, decides, trims and

**Signature:** `reclaimdisk\tick(cwd, deadline, [force]) -> TickResult`

| Parameter  | Type     | Optional | Description |
| ---------- | -------- | -------- | ----------- |
| `cwd`      | `str`    |          |             |
| `deadline` | `double` |          |             |
| `force`    | `bool`   | yes      |             |

**Returns:** TickResult

### reclaimNow

reclaimNow runs a tick in the foreground and prints what really happened. It

**Signature:** `reclaimdisk\reclaimNow()`

