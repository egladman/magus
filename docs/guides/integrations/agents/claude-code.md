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
| read observation | `PreToolUse` on the read tool          |
| MCP              | [MCP](../mcp.md)                       |
| attention events | `Notification`, `Stop`, `SubagentStop` |
| checkpoint       | `Stop`                                 |
| rehydration      | `SessionStart` (`compact`, `resume`)   |
| lease            | `PreToolUse` on the sub-agent tool     |

## Skills

```sh
magus agent install .claude/skills
```

Commit what it writes so every teammate's agent gets the same instructions.
Claude Code discovers skills when a session starts, so restart the session
before it can invoke anything new. [Skills](skills.md) covers the install
surface, the two forms, and the drift check.

## MCP

Configure MCP for Claude Code as a host-level integration; [MCP](../mcp.md) has
the connection and token setup. Tools are discovered at launch, so restart a
client after changing its MCP configuration. An agent that finds MCP unavailable
uses the CLI fallback; it does not manually start Magus solely to obtain tools.

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

## Recording what was read

A third `PreToolUse` entry, matching `Read`, runs
[`magus-guard-observe.sh`](guard-templates.md#magus-guard-observesh). It judges
nothing and prints nothing: it records the path on the activity trail so a later
`magus session show` can say what a session looked at, not only what it changed.

```json
{
  "hooks": {
    "PreToolUse": [
      {
        "matcher": "Read",
        "hooks": [
          {
            "type": "command",
            "command": "sh docs/guides/integrations/agents/magus-guard-observe.sh",
            "timeout": 10
          }
        ]
      }
    ]
  }
}
```

This repository dogfoods it, and it is the one job here that is deliberately not
carried to the other hosts: it changes no verdict, so a reader who skips it loses
detail in a trail rather than enforcement.

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

## Recording where the work stands

Wire `Stop` to [`magus-checkpoint.sh`](guard-templates.md#magus-checkpointsh) and
each time a turn ends magus records the revision, branch and dirtiness of the
tree, plus this session's id and transcript path. `magus session` lists it.

```json
{
  "hooks": {
    "Stop": [
      {
        "hooks": [
          {
            "type": "command",
            "command": "sh docs/guides/integrations/agents/magus-checkpoint.sh",
            "timeout": 10
          }
        ]
      }
    ]
  }
}
```

This is the wiring this repository dogfoods, in `.claude/settings.json` beside
the three guard hooks. It is not a guard: it judges nothing, prints nothing, and
exits 0 whatever happens. `magus session checkpoint --note "..."` writes the same
record by hand, which is the form to reach for when you are the one stopping.

## Handing a compacted session its state back

Claude Code fires `SessionStart` when a session begins, when one is resumed, and
after it compacts a long conversation into a summary; whatever a `SessionStart`
hook prints is added to the model's context. Wire it to
[`magus-rehydrate.sh`](guard-templates.md#magus-rehydratesh) and a session that
just lost its history is handed this checkout instead: branch and revision,
commits not yet on the base ref, the dirty tree split into sources, generated
outputs and unclaimed paths, the live leases with the command that binds each
one, the last recorded run's failures with the ref that holds their output, the
guard wiring, and where the rules live.

```json
{
  "hooks": {
    "SessionStart": [
      {
        "matcher": "compact|resume",
        "hooks": [
          {
            "type": "command",
            "command": "sh docs/guides/integrations/agents/magus-rehydrate.sh",
            "timeout": 10
          }
        ]
      }
    ]
  }
}
```

`compact` is the case that needs it; `resume` gets it for free and answers the
same question, since a resumed session did not watch the tree move while it was
away. Add `startup` if you want it at the top of every session, at the cost of
the block on sessions that would have been fine without it.

Every line is read off the disk when the hook runs, so nothing in it can be a
retelling of a retelling. It restates no rule: the last line names the files your
rules live in, `CLAUDE.md` by default and `REHYDRATE_RULES` when yours is
somewhere else. Run `magus session --brief` yourself to see what a session will
be handed.

## Coverage and limits

No gaps in the guard contract: both surfaces are wired, `deny` arrives as a
`permissionDecision`, and `advise` arrives as `additionalContext`, which puts the
explanation in front of the model rather than the person.

That is not the same as using everything this host offers. `SessionStart` with
matcher `startup`, `UserPromptSubmit`, `PostToolUse`, `PostToolUseFailure`,
`PermissionRequest` and `PreCompact` are all available and all unused, on the test
every wiring here has to pass: a hook must change a verdict or restore state the
model cannot otherwise get. An advisory that fires every turn to restate guidance
the skills already carry fails it.

`SubagentStart` is the one worth naming, because it looks like it should replace
the [lease wiring](#lease-capture) above and does not. It fires when a sub-agent
is spawned and matches on agent type, but its documented input does not carry the
prompt the orchestrator handed over, and the prompt is the whole record: it is
what magus stores as the spawn's payload, and it is where a `lease:` marker rides.
So the `PreToolUse` matcher `Task` wiring stays, on the tool call that does carry
`tool_input.prompt`.

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
