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
| `porcelain` | `any` |          |             |

**Returns:** any

### displayPath

displayPath is path relative to the main worktree when it sits under it.

**Signature:** `reclaimdisk\displayPath(path, main) -> str`

| Parameter | Type  | Optional | Description |
| --------- | ----- | -------- | ----------- |
| `path`    | `any` |          |             |
| `main`    | `any` |          |             |

**Returns:** any

### plural

**Signature:** `reclaimdisk\plural(n, word) -> str`

| Parameter | Type  | Optional | Description |
| --------- | ----- | -------- | ----------- |
| `n`       | `any` |          |             |
| `word`    | `any` |          |             |

**Returns:** any

### age

**Signature:** `reclaimdisk\age(ms) -> str`

| Parameter | Type  | Optional | Description |
| --------- | ----- | -------- | ----------- |
| `ms`      | `any` |          |             |

**Returns:** any

### size

**Signature:** `reclaimdisk\size(kib) -> str`

| Parameter | Type  | Optional | Description |
| --------- | ----- | -------- | ----------- |
| `kib`     | `any` |          |             |

**Returns:** any

### parseCwds

**Signature:** `reclaimdisk\parseCwds(lsof) -> [Cwd]`

| Parameter | Type  | Optional | Description |
| --------- | ----- | -------- | ----------- |
| `lsof`    | `any` |          |             |

**Returns:** any

### pidsByWorktree

pidsByWorktree gives each process to the deepest worktree holding its working directory:

**Signature:** `reclaimdisk\pidsByWorktree(cwds, paths) -> {str:[str]}`

| Parameter | Type  | Optional | Description |
| --------- | ----- | -------- | ----------- |
| `cwds`    | `any` |          |             |
| `paths`   | `any` |          |             |

**Returns:** any

### liveLeases

liveLeases is each checkout's live jobs as "<id> (<state>)". Exited counts as live: the

**Signature:** `reclaimdisk\liveLeases(leases) -> {str:[str]}`

| Parameter | Type  | Optional | Description |
| --------- | ----- | -------- | ----------- |
| `leases`  | `any` |          |             |

**Returns:** any

### classify

classify decides one worktree, listing every reason to keep it rather than the first.

**Signature:** `reclaimdisk\classify(o, main, own, leases, pids, now, keepRecent, shown) -> Item`

| Parameter    | Type  | Optional | Description |
| ------------ | ----- | -------- | ----------- |
| `o`          | `any` |          |             |
| `main`       | `any` |          |             |
| `own`        | `any` |          |             |
| `leases`     | `any` |          |             |
| `pids`       | `any` |          |             |
| `now`        | `any` |          |             |
| `keepRecent` | `any` |          |             |
| `shown`      | `any` |          |             |

**Returns:** any

### branchItems

branchItems lists the agent branches whose loss loses nothing: no kept worktree holds

**Signature:** `reclaimdisk\branchItems(tips, held, removable, unique) -> [Item]`

| Parameter   | Type  | Optional | Description |
| ----------- | ----- | -------- | ----------- |
| `tips`      | `any` |          |             |
| `held`      | `any` |          |             |
| `removable` | `any` |          |             |
| `unique`    | `any` |          |             |

**Returns:** any

### parseUniqueBranches

parseUniqueBranches reads `git log --format=%D --decorate-refs=refs/heads/`: the names

**Signature:** `reclaimdisk\parseUniqueBranches(log) -> [str]`

| Parameter | Type  | Optional | Description |
| --------- | ----- | -------- | ----------- |
| `log`     | `any` |          |             |

**Returns:** any

### rank

rank orders the plan largest reclaim first; unmeasured rows follow, removable before kept.

**Signature:** `reclaimdisk\rank(items) -> [Item]`

| Parameter | Type  | Optional | Description |
| --------- | ----- | -------- | ----------- |
| `items`   | `any` |          |             |

**Returns:** any

### pickChunk

pickChunk is one chunk of the plan: the n removable worktrees untouched longest, the

**Signature:** `reclaimdisk\pickChunk(items, n) -> [Item]`

| Parameter | Type  | Optional | Description |
| --------- | ----- | -------- | ----------- |
| `items`   | `any` |          |             |
| `n`       | `any` |          |             |

**Returns:** any

### totals

**Signature:** `reclaimdisk\totals(items) -> Totals`

| Parameter | Type  | Optional | Description |
| --------- | ----- | -------- | ----------- |
| `items`   | `any` |          |             |

**Returns:** any

### render

**Signature:** `reclaimdisk\render(items) -> [str]`

| Parameter | Type  | Optional | Description |
| --------- | ----- | -------- | ----------- |
| `items`   | `any` |          |             |

**Returns:** any

### guardLine

guardLine is the shell line the guard judges for one removal: the same argv vcs\cmd runs,

**Signature:** `reclaimdisk\guardLine(root, path) -> str?`

| Parameter | Type  | Optional | Description |
| --------- | ----- | -------- | ----------- |
| `root`    | `any` |          |             |
| `path`    | `any` |          |             |

**Returns:** any

### headline

headline is the plan's one-line summary; trimmed, when not empty, is what a cache trim

**Signature:** `reclaimdisk\headline(items, applied, trimmed) -> str`

| Parameter | Type  | Optional | Description |
| --------- | ----- | -------- | ----------- |
| `items`   | `any` |          |             |
| `applied` | `any` |          |             |
| `trimmed` | `any` |          |             |

**Returns:** any

### timeLeft

timeLeft is the timeoutMs a call made now may take: 0, no bound, when deadline is 0, and

**Signature:** `reclaimdisk\timeLeft(deadline, now) -> double`

| Parameter  | Type  | Optional | Description |
| ---------- | ----- | -------- | ----------- |
| `deadline` | `any` |          |             |
| `now`      | `any` |          |             |

**Returns:** any

### status

status reads one worktree's dirt; a directory that is gone reads dirty -1.

**Signature:** `reclaimdisk\status(path, deadline) -> Status`

| Parameter  | Type  | Optional | Description |
| ---------- | ----- | -------- | ----------- |
| `path`     | `any` |          |             |
| `deadline` | `any` |          |             |

**Returns:** any

### survey

survey reads every worktree and agent branch of the repository at the working directory

**Signature:** `reclaimdisk\survey(base, now, keepRecent, measure, deadline) -> Survey`

| Parameter    | Type  | Optional | Description |
| ------------ | ----- | -------- | ----------- |
| `base`       | `any` |          |             |
| `now`        | `any` |          |             |
| `keepRecent` | `any` |          |             |
| `measure`    | `any` |          |             |
| `deadline`   | `any` |          |             |

**Returns:** any

### apply

apply removes the removable rows and returns the plan with each row's result. A worktree

**Signature:** `reclaimdisk\apply(items, root, deadline) -> [Item]`

| Parameter  | Type  | Optional | Description |
| ---------- | ----- | -------- | ----------- |
| `items`    | `any` |          |             |
| `root`     | `any` |          |             |
| `deadline` | `any` |          |             |

**Returns:** any

### trimCache

trimCache trims one cache to its budget, keeping files inside fence.

**Signature:** `reclaimdisk\trimCache(name, dir, trimMb, fence, deadline) -> CacheTrim`

| Parameter  | Type  | Optional | Description |
| ---------- | ----- | -------- | ----------- |
| `name`     | `any` |          |             |
| `dir`      | `any` |          |             |
| `trimMb`   | `any` |          |             |
| `fence`    | `any` |          |             |
| `deadline` | `any` |          |             |

**Returns:** any

### trimLine

trimLine says what one trim really did, null when it did nothing to say.

**Signature:** `reclaimdisk\trimLine(t, fence) -> str?`

| Parameter | Type  | Optional | Description |
| --------- | ----- | -------- | ----------- |
| `t`       | `any` |          |             |
| `fence`   | `any` |          |             |

**Returns:** any

### trimDeclared

trimDeclared trims every cache a spell gives a budget and returns each

**Signature:** `reclaimdisk\trimDeclared(fence, deadline) -> [CacheTrim]`

| Parameter  | Type  | Optional | Description |
| ---------- | ----- | -------- | ----------- |
| `fence`    | `any` |          |             |
| `deadline` | `any` |          |             |

**Returns:** any

### chunk

chunk is the worktree half of a reclaim: the CHUNK_WORKTREES removable

**Signature:** `reclaimdisk\chunk(deadline) -> [Item]`

| Parameter  | Type  | Optional | Description |
| ---------- | ----- | -------- | ----------- |
| `deadline` | `any` |          |             |

**Returns:** any

### keptLine

keptLine counts what a reclaim leaves for a person, "" when it leaves none.

**Signature:** `reclaimdisk\keptLine(items) -> str`

| Parameter | Type  | Optional | Description |
| --------- | ----- | -------- | ----------- |
| `items`   | `any` |          |             |

**Returns:** any

### tick

tick is the one decider every trigger calls. It samples, decides, trims and

**Signature:** `reclaimdisk\tick(cwd, deadline, [force]) -> TickResult`

| Parameter  | Type  | Optional | Description |
| ---------- | ----- | -------- | ----------- |
| `cwd`      | `any` |          |             |
| `deadline` | `any` |          |             |
| `force`    | `any` | yes      |             |

**Returns:** any

### reclaimNow

reclaimNow runs a tick in the foreground and prints what really happened. It

**Signature:** `reclaimdisk\reclaimNow()`

