---
title: magus\guard.spawn
description: Register one Buzz function the agent guard calls on every subagent spawn and continuation, to add a deny or an advisory from your own rules. magus ships the seam and no rules.
tags: [guard, agents, spawn, subagents, policy, magusfile, hooks, strengthen-only]
---

# magus\guard.spawn

`magus\guard.spawn` is the spawn-time sibling of
[`magus\guard.shell`](../guides/integrations/agents/guard.md). The root magusfile
registers one function; the agent guard calls it on every subagent spawn and every
message to an existing subagent it sees, hands it the request, and adds its answer
to the built-in verdict.

**magus ships no spawn rule.** Whether a spawn must name a model, carry completion
criteria, or run in its own worktree is your workspace's decision. The seam exists so
that decision can live in your tree, in Buzz, versioned with the code it governs.

```buzz
import "magus";

magus\guard.spawn(fun (req: SpawnRequest) > SpawnVerdict {
    if (req.kind == "spawn" and req.model == "") {
        return magus\guard.deny("Name a model for this spawn.");
    }
    if (req.target != null and (req.target!.idleMs ?? 0) > 300000 and magus\guard.once("idle-{req.target!.agent}")) {
        return magus\guard.advise("{req.target!.agent} has been idle over five minutes; a fresh brief may be cheaper.");
    }
    return magus\guard.allow();
});
```

## The request

| field         | what it is                                                                            |
| ------------- | ------------------------------------------------------------------------------------- |
| `kind`        | `spawn`, or `continue` for a message to an existing subagent                          |
| `host`        | the host the hook wiring named itself as                                              |
| `session`     | the calling session's id                                                              |
| `model`       | the model the caller named, raw; empty when it named none                             |
| `agentType`   | the subagent type asked for                                                           |
| `description` | the short task label the caller wrote                                                 |
| `name`        | the host's separate addressable name for the new agent                                |
| `prompt`      | the brief for a spawn, the message for a continue                                     |
| `background`  | true only when the caller asked for it                                                |
| `isolated`    | true when the caller asked for its own checkout, such as a worktree                   |
| `parent`      | the description the calling subagent was itself spawned with; empty for a root caller |
| `role`        | `worker` when a lease binds the calling session in this checkout, else `root`         |
| `lease`       | the bound job row for a worker, null for root                                         |
| `target`      | for a continue: the `agent` addressed and `idleMs` since magus last saw it            |

Every field is what the host reported or magus recorded. A host that does not report
a field leaves it empty; nothing is inferred from which host sent the call, and no
model string is mapped to a tier.

`magus\guard.once(key)` is true the first time a key is asked in the calling session.
`magus\guard.count(key)` adds one to a key's tally in that session and returns it.
Both work only while the guard runs the rule.

## Strengthen only

A `deny` blocks the call. An `advise` reaches the agent only where no built-in rule
already spoke. Nothing the function returns lifts a built-in deny, and it is not
called when one stands.

A function that raises, runs past three seconds, or returns something other than a
verdict judges nothing: the built-ins apply alone and the agent is told once per
session that the workspace rule failed. That is the stance `magus\guard.shell` takes
on a rule it cannot use. Registering twice, from a project's magusfile, or with a
non-function stops the workspace load with [MGS1045](codes/magusfile/MGS1045.md).

## Tighten live, loosen on approval

When the magusfile is under version control and a `.buzz` file differs from the
checked-out commit, the guard evaluates the rule twice: once from the working tree and
once from the committed sources, and keeps the stricter answer. An edit that tightens
applies on the next spawn; one that loosens waits for a commit. The committed
side reads each file through the VCS layer rather than a checkout. Local spells
imported by path are read from the working tree on both sides.

## Hosts

| host        | spawn                     | continue          |
| ----------- | ------------------------- | ----------------- |
| Claude Code | `Agent`, `Task`           | `SendMessage`     |
| Cursor      | `subagentStart`           | none the host has |
| Codex       | not wired: no prompt sent | none the host has |
| OpenCode    | not wired                 | not wired         |

Each change to the effective rules is recorded once on the activity trail as a
`guard_policy` event, and every verdict event names the policy digest in force and
which side decided it.
