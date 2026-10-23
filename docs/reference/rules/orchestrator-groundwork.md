---
title: "orchestrator-groundwork: a session that hands out work doing its own groundwork: reads, searches, edits, runs"
description: "A deny rule by default: it refuses a session that hands out work doing its own groundwork: reads, searches, edits, runs, and names what to run instead."
tags: [guard, rules, orchestrator-groundwork, deny]
---

# orchestrator-groundwork

A deny rule by default: it refuses a session that hands out work doing its own groundwork: reads, searches, edits, runs, and names what to run instead.

## What it catches

A session that hands out work doing its own groundwork: reads, searches, edits, runs.

## Why

A session that hands out work keeps to planning and coordinating. Reading and searching source, editing it and running checks belong to the workers it spawns, at any context size: a worker starts small and returns its findings, while every result the orchestrator reads itself stays in the context each later call re-sends. magus judges the role from the live lease, never from the session's history, the context's size or the guard level. A session orchestrates while a row it handed out is declared, running or exited: a row a spawn's title bound, or one `magus job fork` (or magus\job.put) wrote and the job store then showed live. A row the session takes itself with `magus job exec` is no longer handed out, so its `magus job exit` returns the session to orchestrating only what others hold, and once every row has passed or failed the session is judged as any other. That is proven on a main thread its host reported, and inferred on one it did not. Proven, its groundwork is refused; inferred, it is advised once per kind of work. A session with no live row, a leased worker, a subagent and a person are never judged, and the off level leaves the rule on. When no spawn could pass (spawn-names-model at deny, and a harness the model-tier declaration maps no model for), the refusal names `magus job exec <row>` instead of a spawn. Groundwork is a recursive search, a read of a file or of `magus vcs ls-files` inside a checkout of the repository (Markdown included, except AGENTS.md, MAGUS.md, SKILL.md and a host's own instruction file such as ./CLAUDE.md), a full diff, a target run with or without `-s`, a `magus buzz` script that reaches beyond magus\job, a fetch, a write inside a checkout, and integration, one class: merge, rebase, pull, am, reset, stash, restore, checkout, `switch -c`, cherry-pick, `git add`, a commit, the build of ./magus and the generate targets. Integration belongs to the checkout's integrator, the subagent spawned `<parent>/integrate <job>` for a row taken in that checkout; the one exception is `./magus run go-build .` or the bootstrap alone on its line while ./magus is older than its sources or absent, which the root runs itself. Orchestration work passes: the job verbs, graph reads (`magus query`, `explain`, `refs`, `path`, `describe`, `affected --plan`), branch work (a plain `git switch`, `branch` and `worktree`), the final gate, the check of a live row the session forked, `git push`, `gh pr`, `magus query output` of a guard verdict or of a row's recorded check output, short status, and reads, searches and writes of paths outside every checkout, such as a plan or a memory, with a `~/` path placed under the home directory. A write while its checkout is midway through a merge or a cherry-pick is advised and never refused, since resolving a conflict is integration. Set to advise, proof is advised too.

## Default and override

By default this rule takes the decision `deny`. A workspace sets it by name, in its root
magusfile, to `deny`, `advise` or `off`:

```buzz
magus\guard.builtins({"orchestrator-groundwork": "advise"})
```

A loosening takes effect once it is committed; a tightening applies at once.

## Seeing it

A verdict names its rule in brackets, which is how you got here:

```text
deny [orchestrator-groundwork]: ...
```

A workspace that sets this rule to `advise` sees `advise [orchestrator-groundwork]: ...` instead.

`magus describe rule orchestrator-groundwork` prints the same entry at a terminal, and
`magus describe rules` lists every rule this workspace enforces.

## See also

- [All rules](index.md) - what this workspace enforces, deny first
- [The guard](../../guides/integrations/agents/guard.md) - how a verdict is reached and wired
- [Setting a built-in rule](../../guides/integrations/agents/guard.md#setting-a-built-in-rule) - how a workspace changes this default
