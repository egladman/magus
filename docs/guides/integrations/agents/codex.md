---
title: Codex
description: Wiring magus into Codex - skills in .agents/skills, the AGENTS.md block you paste, MCP in the user-level config, and the experimental hooks that carry the guard.
tags: [agents, codex, skills, AGENTS.md, guard, hooks, MCP]
---

# Codex

Codex reads two things, and it wants both: `.agents/skills/` for focused
workflows it loads on demand, and `AGENTS.md` for guidance that is always on.
Its hook event and reply match Claude Code's, so the guard needs no script of
its own - a wiring file points at the same two templates.

| what            | where                                                    |
| --------------- | -------------------------------------------------------- |
| skills          | `.agents/skills/`                                        |
| always-on rules | `AGENTS.md` (you paste the block; magus never writes it) |
| guard wiring    | `~/.codex/hooks.json`, `PreToolUse`                      |
| command surface | deny reaches the model; advise is not sent               |
| file surface    | deny reaches the model; advise is not sent               |
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
magus server start
export MAGUS_MCP_TOKEN="$(magus config mcp token print)"
codex mcp list
magus status --probe=liveness,mcp
```

`codex mcp list` confirms configuration; the probe confirms the endpoint is
serving. [MCP](../mcp.md) covers dedicated connector tokens, the ChatGPT desktop
app, and what to do when `mcp.address` changes.

## Guard hook

Hooks are experimental and off by default. Turn them on in `~/.codex/config.toml`:

```toml
[features]
codex_hooks = true
```

Then save this as `~/.codex/hooks.json` (or `.codex/hooks.json`), with the paths
pointing at wherever you put your copies of the two templates:

```json
{
  "hooks": {
    "PreToolUse": [
      {
        "matcher": "Bash",
        "hooks": [
          {
            "type": "command",
            "command": "GUARD_AGENT_NAME=codex GUARD_NO_ADVISE=1 sh docs/guides/integrations/agents/magus-guard-command.sh",
            "statusMessage": "magus guard: checking command"
          }
        ]
      },
      {
        "matcher": "Edit|Write",
        "hooks": [
          {
            "type": "command",
            "command": "GUARD_AGENT_NAME=codex GUARD_NO_ADVISE=1 sh docs/guides/integrations/agents/magus-guard-path.sh",
            "statusMessage": "magus guard: checking file"
          }
        ]
      }
    ]
  }
}
```

`GUARD_AGENT_NAME` labels the observation magus records; it cannot change a
verdict. Everything else about the two scripts is described in
[Guard hook templates](guard-templates.md), including the variables that let one
implementation serve several hosts.

## Notifications

Codex runs a program on its notify setting. Shape the event into the canonical
envelope and pipe it to `magus session notify`, exactly as the other hosts do - see
[Attention hooks](notifications.md) for the envelope and the vocabulary.

## Coverage and limits

- Hooks are experimental, off by default, and not available on Windows.
- Reports disagree on whether `apply_patch`, `Edit` and `Write` fire
  `PreToolUse`. OpenAI's hooks page says they do; at least one third-party
  reference says `Bash` only. Treat the second matcher as provisional and
  confirm it against the current documentation.
- `apply_patch` delivers a PATCH in `tool_input.command`, not a file path, so
  the declared-output guard wants the `Edit`/`Write` matcher rather than
  `apply_patch`.
- The command rules are executed against this binary with a real event. The
  file surface is wired per OpenAI's documentation and has not been executed
  here.
- Delegation capture is UNVERIFIED here. Codex's `PreToolUse` envelope is the
  same shape magus already reads, so a delegation would be captured by the
  wiring on [Claude Code](claude-code.md#delegation-capture) with the matcher
  changed - but nothing in Codex's documented tool surface is known to hand a
  sub-agent a prompt, so there is no matcher to name and none has been executed.

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
