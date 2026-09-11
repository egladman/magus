---
title: Codex
description: Wiring magus into Codex - skills in .agents/skills, the AGENTS.md block you paste, MCP in the user-level config, and the hooks that carry the guard, the checkpoint and the post-compaction brief.
tags: [agents, codex, skills, AGENTS.md, guard, hooks, MCP]
---

# Codex

Codex reads two things, and it wants both: `.agents/skills/` for focused
workflows it loads on demand, and `AGENTS.md` for guidance that is always on.
Its hook events and replies match Claude Code's, so nothing here needs a script of
its own: one wiring file points at the same shipped templates.

| what            | where                                                    |
| --------------- | -------------------------------------------------------- |
| skills          | `.agents/skills/`                                        |
| always-on rules | `AGENTS.md` (you paste the block; magus never writes it) |
| guard wiring    | `~/.codex/hooks.json`, `PreToolUse`                      |
| command surface | deny and advise both reach the model                     |
| file surface    | deny and advise both reach the model                     |
| checkpoint      | `Stop`                                                   |
| rehydration     | `SessionStart` (`compact`)                               |
| MCP             | `~/.codex/config.toml`, see [MCP](../mcp.md)             |

## Skills

```sh
magus agent install .agents/skills
```

The same install reads your `AGENTS.md` and prints the managed magus block when
it is missing or stale, for you to paste between its markers. Nothing writes
that file. [Skills](skills.md) explains why, and what `magus doctor` does
with the pasted block afterwards.

Codex discovers skills, `AGENTS.md`, and MCP servers at task start, so start a
new task after changing any of them.

## MCP

Register the daemon in your user-level `~/.codex/config.toml`, never in the
repository:

```toml
[mcp_servers.magus]
url = "http://127.0.0.1:7391/mcp"
bearer_token_env_var = "MAGUS_MCP_TOKEN"
enabled = true
```

```sh
export MAGUS_MCP_TOKEN="$(magus config token print)"
codex mcp list
magus status --probe=liveness,mcp
```

`codex mcp list` confirms configuration; the probe confirms the endpoint is
serving. [MCP](../mcp.md) covers dedicated connector tokens, the ChatGPT desktop
app, and what to do when `mcp.address` changes. This is user-owned host setup;
an agent that cannot reach MCP uses the CLI fallback and does not manually start
Magus solely to obtain tools.

## Guard hook

Check that hooks are on before wiring anything:

```sh
codex features list
```

The `hooks` row reports the stage and whether it is enabled. It is stable and on
by default as of codex-cli 0.145.0; older builds gated it behind a feature flag
in `~/.codex/config.toml`, so read the row rather than trusting this paragraph.

Save this as `~/.codex/hooks.json` (or `.codex/hooks.json`), with the paths
pointing at wherever you put your copies of the templates:

```json
{
  "hooks": {
    "PreToolUse": [
      {
        "matcher": "Bash",
        "hooks": [
          {
            "type": "command",
            "command": "GUARD_AGENT_NAME=codex sh docs/guides/integrations/agents/magus-guard-command.sh",
            "statusMessage": "magus guard: checking command"
          }
        ]
      },
      {
        "matcher": "Edit|Write",
        "hooks": [
          {
            "type": "command",
            "command": "GUARD_AGENT_NAME=codex sh docs/guides/integrations/agents/magus-guard-path.sh",
            "statusMessage": "magus guard: checking file"
          }
        ]
      }
    ],
    "SessionStart": [
      {
        "matcher": "compact",
        "hooks": [
          {
            "type": "command",
            "command": "REHYDRATE_FORMAT=json REHYDRATE_RULES=AGENTS.md sh docs/guides/integrations/agents/magus-rehydrate.sh",
            "statusMessage": "magus: handing this checkout back"
          }
        ]
      }
    ],
    "Stop": [
      {
        "hooks": [
          {
            "type": "command",
            "command": "GUARD_AGENT_NAME=codex sh docs/guides/integrations/agents/magus-checkpoint.sh",
            "statusMessage": "magus: recording where the work stands"
          }
        ]
      }
    ]
  }
}
```

`GUARD_AGENT_NAME` labels the observation magus records; it cannot change a
verdict. Everything else about the four scripts is described in
[Guard hook templates](guard-templates.md), including the variables that let one
implementation serve several hosts.

The two `PreToolUse` entries used to carry `GUARD_NO_ADVISE=1`, which rendered
every advisory as nothing. That rested on a claim OpenAI's current hooks
reference contradicts: `additionalContext` is a supported `PreToolUse` field, and
the response keys Codex rejects, the ones that make it mark a hook run failed and
continue the call, are `continue`, `stopReason` and `suppressOutput`. Suppression
cost this host every explanation the guard had to give while enforcing every deny,
which is the half of the contract nothing in a session reports missing. If your own
build behaves otherwise, `GUARD_NO_ADVISE=1` still suppresses the arm.

The `Stop` entry is not a guard. It records where the work stands each time a
turn ends, which is worth having here in particular: a Codex session that runs
out of usage stops mid-task, and the transcript it leaves behind is addressed by
a session id nobody wrote down. `magus session` lists what it recorded, and
`magus session checkpoint --note "..."` is the same record made by hand.

## Handing a compacted session its state back

The `SessionStart` entry matches `compact`, the moment Codex replaces a long
session's history with a summary, and runs
[`magus-rehydrate.sh`](guard-templates.md#magus-rehydratesh). What it prints is
this checkout: branch and revision, commits not yet on the base ref, the dirty
tree split into sources, generated outputs and unclaimed paths, the live leases,
the last recorded run's failures, and where the rules live.

Two variables shape it for this host. `REHYDRATE_FORMAT=json` is required, not
cosmetic: Codex reads a `SessionStart` hook's stdout as a JSON reply and adds
`hookSpecificOutput.additionalContext` to the developer context, so plain text
arrives nowhere. `REHYDRATE_RULES=AGENTS.md` names the instruction file Codex
actually reads, since the template's default is another host's.

`compact` is the matcher wired here, matching what this repository dogfoods on
[Claude Code](claude-code.md#handing-a-compacted-session-its-state-back): it is
the moment state was LOST. Codex offers `startup` and `resume` as well; `resume`
answers the related question, since a session resumed after a break did not watch
the tree move while it was away.

Run `magus session --brief` yourself to read exactly what a compacted session
will be handed.

## Notifications

Codex runs a program on its notify setting. Shape the event into the canonical
envelope and pipe it to `magus session notify`, exactly as the other hosts do - see
[Attention hooks](notifications.md) for the envelope and the vocabulary.

## Coverage and limits

- Hooks are not available on Windows, and `[features] hooks = false` turns the
  whole surface off. A repo-local `.codex/hooks.json` is also inert until you
  trust the project layer, and every non-managed hook wants a per-hash review
  through `/hooks`, so a config committed to a repository guards nobody who has
  not accepted it. That is the one way this host differs from the others in kind
  rather than degree.
- Reports disagree on whether `apply_patch`, `Edit` and `Write` fire
  `PreToolUse`. OpenAI's hooks page says they do; at least one third-party
  reference says `Bash` only. Treat the second matcher as provisional and
  confirm it against the current documentation.
- `apply_patch` delivers a PATCH in `tool_input.command`, not a file path, so
  the declared-output guard wants the `Edit`/`Write` matcher rather than
  `apply_patch`.
- The command rules are executed against this binary with a real event. The
  file surface is wired per OpenAI's documentation and has not been executed
  here, and neither is the `SessionStart` envelope: `REHYDRATE_FORMAT=json`
  renders the shape the reference documents, against no live Codex.
- **Lease capture has an event but no context.** `SubagentStart` exists and
  matches on `agent_type`, which retires the older claim here that Codex had no
  sub-agent lifecycle at all. What its payload carries is `agent_id`,
  `agent_type` and `permission_mode`: identity, not the work handed over. magus
  records a spawn from the PROMPT an orchestrator hands a sub-agent, because the
  prompt is the thing worth keeping and the thing a `lease:` marker rides in, so
  there is nothing in this event to record. If a Codex tool ever hands a
  sub-agent a prompt through `tool_input`, the wiring on
  [Claude Code](claude-code.md#lease-capture) captures it here unchanged.

## Verify

```sh
codex mcp list
magus status --probe=liveness,mcp
magus doctor
```

doctor's **agent skills** check grades the installed skills and the pasted `AGENTS.md` block
against the running binary. `doctor`'s **guard binary** and **guard wiring**
checks report which binary a hook resolves and whether any host config actually
invokes a current template.
