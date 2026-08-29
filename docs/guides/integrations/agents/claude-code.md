---
title: Claude Code
description: Wiring magus into Claude Code - skills in .claude/skills, the two PreToolUse guard hooks, attention notifications, and the checks that prove the guard is running.
tags: [agents, claude, claude code, skills, guard, hooks, notifications]
---

# Claude Code

Claude Code reads Agent Skills from `.claude/skills/` and runs a `PreToolUse`
hook before every tool call. That covers both guard surfaces, and both verdicts
reach the model, so nothing in the contract is lost here. It is also the setup
this repository dogfoods and the only one executed end to end against a real
event.

| what             | where                                  |
| ---------------- | -------------------------------------- |
| skills           | `.claude/skills/`                      |
| guard wiring     | `.claude/settings.json`, `PreToolUse`  |
| command surface  | deny and advise both reach the model   |
| file surface     | deny and advise both reach the model   |
| MCP              | [MCP](../mcp.md)                       |
| attention events | `Notification`, `Stop`, `SubagentStop` |
| lease            | `PreToolUse` on the sub-agent tool     |

## Skills

```sh
magus agent install .claude/skills
```

Commit what it writes so every teammate's agent gets the same instructions.
Claude Code discovers skills when a session starts, so restart the session
before it can invoke anything new. [Skills](skills.md) covers the install
surface, the two permutations, and the drift check.

## MCP

```sh
magus server start
```

The daemon serves MCP on `http://127.0.0.1:7391/mcp`; [MCP](../mcp.md) has the
token and client setup. Tools are discovered at launch, so a client already
running when the daemon comes up sees them only after a restart.

## Guard hook

Two `PreToolUse` entries: one matching `Bash` for the command rules, one
matching the file-editing tools for the declared-output and notes rules. Both
run a template you own - download them from
[Guard hook templates](guard-templates.md) and point the config at your copies.

```json
{
  "hooks": {
    "PreToolUse": [
      {
        "matcher": "Bash",
        "hooks": [{ "type": "command", "command": "sh ~/.claude/hooks/magus-guard-command.sh", "timeout": 10 }]
      },
      {
        "matcher": "Edit|Write|NotebookEdit",
        "hooks": [{ "type": "command", "command": "sh ~/.claude/hooks/magus-guard-path.sh", "timeout": 10 }]
      }
    ]
  }
}
```

This repository's own `.claude/settings.json` points at the templates in
`docs/guides/integrations/agents/` rather than at a private copy, and a test
fails if it stops doing so. What magus dogfoods is what you download.

`magus session hook` also reads Claude Code's event JSON directly: `tool_input.command`,
`tool_input.file_path`, `session_id` and `hook_event_name` are the fields it
knows, and a payload carrying a file path is judged as a write without `--path`.
So one command serves both matchers, with no `jq` and no script:

```sh
magus session hook -o 'template={{if eq .decision "deny"}}{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":{{toJson .reason}}}}{{else if eq .decision "advise"}}{"hookSpecificOutput":{"hookEventName":"PreToolUse","additionalContext":{{toJson .context}}}}{{end}}'
```

What it trades away is the templates' handling of a magus that is missing or too
old to judge: both of those render nothing, and Claude Code reads nothing as
allow, so the session goes unguarded with no sign of it. Use the short form
while you are experimenting; use the templates once you rely on the guard.

## Lease capture

When Claude Code hands work to a sub-agent it does so through a tool call, and
that call fires `PreToolUse` like any other - carrying the whole prompt the
orchestrator is handing over in `tool_input.prompt`. Neither guard matcher above
selects it, so by default magus never sees a lease. Add a third entry to
record one:

```json
{
  "hooks": {
    "PreToolUse": [
      {
        "matcher": "Task",
        "hooks": [
          {
            "type": "command",
            "command": "d=$PWD; while [ -n \"$d\" ] && [ ! -f \"$d/magusfile.buzz\" ]; do d=${d%/*}; done; GUARD_MAGUS_BIN=$([ -x \"$d/magus\" ] && printf %s \"$d/magus\" || command -v magus 2>/dev/null); [ -n \"$GUARD_MAGUS_BIN\" ] && \"$GUARD_MAGUS_BIN\" session hook --agent-name claude-code >/dev/null 2>&1; exit 0",
            "timeout": 10
          }
        ]
      }
    ]
  }
}
```

No `jq` and no template: the whole event goes in unchanged, and magus reads
`tool_input.prompt` for the context, `tool_input.subagent_type` (then
`description`, then `tool_name`) for the callee's label, and `session_id` for the
parent's session. The result is one `agent_spawn` event per lease, with the
handed context stored as a payload blob you fetch by ref.

The `while` loop at the front is how it finds magus, and it is doing the same job
as the templates' longer version: walk up from the hook's working directory to
the nearest `magusfile.buzz`, prefer that workspace's own `./magus`, and fall
back to `PATH`. A hook runs in the SESSION's directory, which is not always the
workspace root - open a session one level down and a plain `./magus` is not
there. It falls through to `PATH` silently, and where the `PATH` copy cannot load
the workspace, the event is simply never recorded. Nothing surfaces that: an
audit trail with holes reads exactly like one nobody wrote to.

It records; it does not judge. A lease prompt is prose, so the command rules
never run against it and the verdict is always a pass - a prompt that mentions a
denied command describes it rather than runs it. Output is discarded and the exit
status is forced to 0 for the same reason the notification hook does it: an audit
step must not be able to break the session it observes.

To join those events to a ledger, write the marker line documented in
[Any other host](any-host.md#lease-capture) at the top of the prompt you
hand the sub-agent.

## Notifications

`magus session notify` turns a host event into a desktop notification. It does not
send an event to the daemon or Console. Wire `Notification` (it fires on a
permission prompt and when the agent goes idle waiting for input), and `Stop` or
`SubagentStop` for completion.

```json
{
  "hooks": {
    "Notification": [
      {
        "hooks": [
          {
            "type": "command",
            "command": "d=$PWD; while [ -n \"$d\" ] && [ ! -f \"$d/magusfile.buzz\" ]; do d=${d%/*}; done; GUARD_MAGUS_BIN=$([ -x \"$d/magus\" ] && printf %s \"$d/magus\" || command -v magus 2>/dev/null); [ -n \"$GUARD_MAGUS_BIN\" ] && jq -c '{schema_version: 1, outcome: .hook_event_name, source: {kind: \"agent\"}, message: .message}' | \"$GUARD_MAGUS_BIN\" session notify --desktop >/dev/null 2>&1; exit 0"
          }
        ]
      }
    ]
  }
}
```

It exits 0 and swallows its own output on purpose: a notifier that can fail is a
hook that can break the session it was meant to watch. It opens with the same
magusfile walk as the lease hook above, for the same reason.
[Attention hooks](notifications.md) covers the envelope and the outcome vocabulary.

## Coverage and limits

No gaps. Both guard surfaces are wired, `deny` arrives as a
`permissionDecision`, and `advise` arrives as `additionalContext`, which is the
only channel that puts an explanation in front of the model rather than the
person.

## Verify

```sh
magus doctor
```

`doctor`'s **guard binary** check names the binary a hook would actually run and
fails when it is older than your working tree; **guard wiring** runs a canary
command through it and then looks for a host config that invokes a current
template; **agent skills** grades the installed copies against the running binary
and `--fix` reinstalls whatever it reports stale.

Commit `.claude/settings.json` once you are happy with it. Until a checkout has
that file, its guard rules are correct and entirely unenforced, with nothing in
the session saying so - which is the gap the **guard wiring** check exists to
report.
