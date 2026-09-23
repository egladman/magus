---
title: magus\guard.spawn
description: Register one Buzz function the agent guard calls on every subagent spawn and continuation, to add a deny or an advisory from your own rules. magus ships the seam and no rules.
tags: [guard, agents, spawn, subagents, policy, magusfile, hooks, strengthen-only, attribution, context]
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
    if (req.target != null and (req.target!.contextTokens ?? 0) > 200000) {
        return magus\guard.deny("{req.target!.agent} carries over 200k tokens of context; spawn fresh with a narrow brief.");
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
| `target`      | for a continue: the agent addressed and what magus recorded about it (below)          |

`target` is null on a spawn. On a continue:

| field           | what it is                                                                                        |
| --------------- | ------------------------------------------------------------------------------------------------- |
| `agent`         | the name or id the caller addressed                                                               |
| `idleMs`        | milliseconds since magus last saw it spawned, continued or finish its spawn call; null when never |
| `description`   | the title the agent was spawned with; empty when magus never saw that spawn finish                |
| `model`         | the model that spawn named, raw; empty when it named none                                         |
| `contextTokens` | the agent's last observed context size in tokens; null when the host reported no usage for it     |

`contextTokens` is input plus cache-read plus cache-write tokens from the latest usage
record the host reported for that agent: what the model was handed on its last call,
which is the context a resume has to rebuild. Claude Code reports it through
`SubagentStop`, whose `agent_transcript_path` magus reads for its last usage record,
looking at the file's final 512 KiB only. Cursor and Codex report no per-agent usage,
so it stays null there.

Every field is what the host reported or magus recorded. A host that does not report
a field leaves it empty; nothing is inferred from which host sent the call, and no
model string is mapped to a tier.

`magus\guard.once(key)` is true the first time a key is asked in the calling session.
`magus\guard.count(key)` adds one to a key's tally in that session and returns it.
Both work only while the guard runs the rule.

`magus\job\list()` works inside the rule and answers from the job rows the guard read
for this call, so the rule and the built-in verdict it adds to read the same store.
Every job member that writes raises inside a rule.

## Job attribution

A spawn whose title (`description`) reads `<parent>/<role> <job>`, where `<job>` names
a declared, running or exited job, attributes the new agent to that job. From then on
every hook call carrying that agent's id is graded under the job's lease. An explicit
`--lease` still wins. The agent's job outranks the session's `magus job exec` binding,
because a subagent shares its parent's session id and only its agent id tells the two
apart, and it outranks a `BAGGAGE` claim, as every record does.

When the job has not reported a base and the spawn did not ask for its own checkout,
magus records this checkout's revision and dirty-patch digest for it, the values
`magus job exec` records, so the agent's first write is not refused for a missing exec.
An isolated agent's checkout is one magus cannot see from the spawning side, so it
reports its own base with `magus job exec`.

Attribution is recorded when the host reports the finished spawn call with the child's
id. Claude Code reports that at launch for a background agent, and only on return for
a foreground one, so a foreground agent's own calls run unattributed. A host with no
agent id keeps `BAGGAGE` and `magus job exec`.

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

| host        | spawn                     | continue          | `contextTokens` |
| ----------- | ------------------------- | ----------------- | --------------- |
| Claude Code | `Agent`, `Task`           | `SendMessage`     | `SubagentStop`  |
| Cursor      | `subagentStart`           | none the host has | not reported    |
| Codex       | not wired: no prompt sent | none the host has | not reported    |
| OpenCode    | not wired                 | not wired         | not wired       |

Each change to the effective rules is recorded once on the activity trail as a
`guard_policy` event, and every verdict event names the policy digest in force and
which side decided it.
