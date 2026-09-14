---
title: Codex
description: Wiring magus into Codex - skills in .agents/skills, the AGENTS.md block you paste, MCP in the user-level config, and the hooks that carry the guard, the checkpoint and the post-compaction brief.
tags: [agents, codex, skills, AGENTS.md, guard, hooks, MCP]
---

# Codex

Codex reads two things, and it wants both: `.agents/skills/` for focused
workflows it loads on demand, and `AGENTS.md` for guidance that is always on.
Its hook payloads differ from Claude Code's, but both hosts accept the same
verdict shape. Current Magus binaries therefore receive those events directly;
the shared templates remain the portable fallback for hosts without that host
contract.

| what             | where                                                          |
| ---------------- | -------------------------------------------------------------- |
| skills           | `.agents/skills/`                                              |
| always-on rules  | `AGENTS.md` (you paste the block; magus never writes it)       |
| guard wiring     | `.codex/hooks.json`, `PreToolUse`                              |
| command surface  | deny and advise both reach the model                           |
| file surface     | deny and advise both reach the model                           |
| MCP call surface | `PreToolUse` (`mcp__.*`), deny and advise both reach the model |
| checkpoint       | `Stop`                                                         |
| rehydration      | `SessionStart` (`compact`)                                     |
| MCP              | `~/.codex/config.toml`, see [MCP](../mcp.md)                   |

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

The `hooks` row reports the stage and whether it is enabled. Hooks are stable and
enabled by default; read the row when working with an older local build.

The shipped `codex` harness descriptor owns the host `PreToolUse` wiring: its
workspace config path, matchers, and response template are data in
`harnesses/codex.json`. Apply and verify it from the workspace root. The
lifecycle entries below remain portable scripts because they are not guard
verdicts:

```sh
magus agent harness apply --host codex
magus agent harness verify --host codex
```

The descriptor writes `.codex/hooks.json` and installs the guard entries
shown here:

```json
{
  "hooks": {
    "PreToolUse": [
      {
        "matcher": "Bash",
        "hooks": [
          {
            "type": "command",
            "command": "guard_root=$PWD; while [ -n \"$guard_root\" ]; do if [ -f \"$guard_root/magusfile.buzz\" ]; then if [ -x \"$guard_root/magus\" ]; then exec \"$guard_root/magus\" agent hook --host codex; fi; break; fi; guard_root=${guard_root%/*}; done; exec magus agent hook --host codex",
            "statusMessage": "magus guard: checking command"
          }
        ]
      },
      {
        "matcher": "Edit|Write",
        "hooks": [
          {
            "type": "command",
            "command": "guard_root=$PWD; while [ -n \"$guard_root\" ]; do if [ -f \"$guard_root/magusfile.buzz\" ]; then if [ -x \"$guard_root/magus\" ]; then exec \"$guard_root/magus\" agent hook --host codex; fi; break; fi; guard_root=${guard_root%/*}; done; exec magus agent hook --host codex",
            "statusMessage": "magus guard: checking file"
          }
        ]
      },
      {
        "matcher": "mcp__.*",
        "hooks": [
          {
            "type": "command",
            "command": "guard_root=$PWD; while [ -n \"$guard_root\" ]; do if [ -f \"$guard_root/magusfile.buzz\" ]; then if [ -x \"$guard_root/magus\" ]; then exec \"$guard_root/magus\" agent hook --host codex; fi; break; fi; guard_root=${guard_root%/*}; done; exec magus agent hook --host codex",
            "statusMessage": "magus guard: checking MCP tool call"
          }
        ]
      },
      {
        "matcher": "Read",
        "hooks": [
          {
            "type": "command",
            "command": "guard_root=$PWD; while [ -n \"$guard_root\" ]; do if [ -f \"$guard_root/magusfile.buzz\" ]; then if [ -x \"$guard_root/magus\" ]; then exec \"$guard_root/magus\" agent hook --host codex --observe; fi; break; fi; guard_root=${guard_root%/*}; done; exec magus agent hook --host codex --observe",
            "statusMessage": "magus: recording read"
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
            "command": "REHYDRATE_FORMAT=json REHYDRATE_RULES=AGENTS.md sh \"$(magus describe projects -o 'template={{.workspace}}')/docs/guides/integrations/agents/magus-rehydrate.sh\"",
            "statusMessage": "magus: restating this checkout's rules"
          }
        ]
      }
    ],
    "Stop": [
      {
        "hooks": [
          {
            "type": "command",
            "command": "GUARD_AGENT_NAME=codex sh \"$(magus describe projects -o 'template={{.workspace}}')/docs/guides/integrations/agents/magus-checkpoint.sh\"",
            "statusMessage": "magus: recording where the work stands"
          }
        ]
      }
    ]
  }
}
```

`magus agent hook` derives the path, command, read, and MCP surfaces from the
raw event and emits Codex's hook reply. It replaces the
`magus-guard-command.sh`, `magus-guard-path.sh`, and read-observer templates.
The generated adapter walks up to the workspace root, prefers that checkout's
`./magus`, and falls back to `PATH` only when no workspace binary is executable.

The two `PreToolUse` entries used to carry `GUARD_NO_ADVISE=1`, which rendered
every advisory as nothing. That rested on a claim OpenAI's current hooks
reference contradicts: `additionalContext` is a supported `PreToolUse` field, and
the response keys Codex rejects, the ones that make it mark a hook run failed and
continue the call, are `continue`, `stopReason` and `suppressOutput`. Suppression
cost this host every explanation the guard had to give while enforcing every deny,
which is the half of the contract nothing in a session reports missing. If your own
build behaves otherwise, `GUARD_NO_ADVISE=1` still suppresses the arm.

### Maintaining the workspace harness

When repeated guard evidence identifies an outdated Codex harness, review the
proposal with `magus agent improve`, then explicitly apply it:

```sh
magus agent improve --apply --host codex
```

That command delegates the write to the `codex` descriptor. It updates only its
Magus-owned host `PreToolUse` entries in the descriptor's workspace
configuration path; it preserves unrelated settings and does not touch
user-level configuration, lifecycle entries, templates, compiled guard rules,
skills, memory, or `AGENTS.md`. Review the normal JSON diff before committing
it, then run `magus agent harness verify --host codex`.

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
When repeated guard feedback needs review, the same brief adds one bounded line
that directs the model to `magus agent improve`; it does not edit memory or
instructions by itself.

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

- **The MCP call surface is wired.** The descriptor's third matcher receives
  the complete `tool_input` object and sends the raw event to the same host
  adapter as the command and file surfaces. It is transport-complete, but no
  shipped rule currently judges arbitrary MCP tool names or parameters.
- Hooks are not available on Windows, and `[features] hooks = false` turns the
  whole surface off. A repo-local `.codex/hooks.json` is also inert until you
  trust the project layer, and every non-managed hook wants a per-hash review
  through `/hooks`, so a config committed to a repository guards nobody who has
  not accepted it. That is the one way this host differs from the others in kind
  rather than degree.
- Codex documents `PreToolUse` for `Bash`, `apply_patch`, `Edit`, and `Write`.
  `apply_patch` carries a patch in `tool_input.command`, rather than a file path,
  so the declared-output guard intentionally matches `Edit|Write`.
- The command rules are exercised by this repository's harness. The file and
  MCP surfaces are wired to the documented events but still need a live Codex
  event in this workspace to make the observation empirical; the `SessionStart`
  envelope is likewise rendered from the documented JSON contract.
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

doctor's **agent skills** check grades the installed skills and the pasted
`AGENTS.md` block against the running binary. **guard wiring** loads each
available harness descriptor and reports the explicit `verified`, `uncovered`,
or `invalid` result rather than guessing at host config locations.
