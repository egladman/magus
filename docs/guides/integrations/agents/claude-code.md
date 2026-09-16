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

| what             | where                                                         |
| ---------------- | ------------------------------------------------------------- |
| skills           | `.claude/skills/`                                             |
| guard wiring     | `.claude/settings.json`, `PreToolUse`                         |
| command surface  | deny and advise both reach the model                          |
| file surface     | deny and advise both reach the model                          |
| MCP call surface | deny and advise both reach the model                          |
| read observation | `PreToolUse` on the read tool                                 |
| MCP              | [MCP](../mcp.md)                                              |
| attention events | `Notification`, `Stop`, `SubagentStop`                        |
| checkpoint       | `Stop`                                                        |
| rehydration      | `SessionStart` (`compact`, `resume`)                          |
| lease            | `PreToolUse` on the sub-agent tool                            |
| declared model   | `PreToolUse` on the sub-agent tool, when the caller named one |

## Skills

```sh
magus agent install .claude/skills
```

Commit what it writes so every teammate's agent gets the same instructions.
Claude Code discovers skills when a session starts, so restart the session
before it can invoke anything new. [Skills](skills.md) covers the install
surface, the two forms, and the drift check.

## MCP

Configure MCP for Claude Code yourself. `magus agent harness apply --id
claude-code` prints the `claude mcp add` sketch only. Resolve secret ref
`MAGUS_MCP_TOKEN` (env provider by default) for the bearer header; [MCP](../mcp.md)
has the full token setup. Tools are discovered at launch, so restart a client
after changing its MCP configuration. An agent that finds MCP unavailable uses
the CLI fallback; it does not manually start Magus solely to obtain tools.

## Guard hook

Prefer wiring the Claude Code harness from the root magusfile when you bounce
between hosts; apply then covers every wired provider:

```buzz
import "spells/harness/claude-code" as claude
magus\harness.provider(claude)
```

```sh
magus agent harness apply
magus agent harness verify
```

To adapt that Buzz harness without modifying Magus source: copy the spell into
the workspace, change only the import path (for example
`import "harness/claude-code" as claude`), edit the workspace Buzz, then re-run
apply and verify. Details:
[Adapting a Buzz harness](../../../reference/skills/magus-workspace-rules.md) and
[Improving recurring friction](guard.md#improving-recurring-friction).

Or target Claude Code alone (spell or `harnesses/claude-code.json` fallback):

```sh
magus agent harness apply --id claude-code
magus agent harness verify --id claude-code
```

The spell installs entries for commands, file edits, Magus MCP tool calls, read
observation, and sub-agent spawns. Each runs a shipped script that talks to
`magus session hook`:

```json
{
  "hooks": {
    "PreToolUse": [
      {
        "matcher": "Bash",
        "hooks": [{ "type": "command", "command": "sh docs/guides/integrations/agents/magus-hook-command.sh", "timeout": 10 }]
      },
      {
        "matcher": "Edit|Write|NotebookEdit",
        "hooks": [{ "type": "command", "command": "sh docs/guides/integrations/agents/magus-hook-path.sh", "timeout": 10 }]
      }
    ]
  }
}
```

This repository's own `.claude/settings.json` invokes those same files. The
scripts are the glue; harness apply only merges the fragments that name them.
See [guard templates](guard-templates.md) for the files and the variables that
adapt them.

### Maintaining the workspace harness

This host is a Buzz harness spell. Adapt without Magus source edits by forking
the spell and changing only the import path; then `magus agent harness apply`
and `verify`. See
[Adapting a Buzz harness](../../../reference/skills/magus-workspace-rules.md) and
[Improving recurring friction](guard.md#improving-recurring-friction).

`harnesses/claude-code.json` remains as a fallback when the magusfile does not
wire the spell. Recurring Magus-owned fragment merges for that JSON path still
use:

```sh
magus agent improve --apply --id claude-code
```

That writes only Magus-owned native `PreToolUse` entries in the workspace-local
JSON configuration and preserves every other setting. It never writes
user-level configuration, templates, compiled guard rules, skills, memory, or
`AGENTS.md`. Review the JSON diff, then `magus agent harness verify --id
claude-code`.

## MCP tool calls

Claude Code's `PreToolUse` also fires for a tool served over MCP, matching
`mcp__<server>__<tool>`. The shipped descriptor includes a Magus-MCP matcher,
so `magus agent harness apply --id claude-code` installs this entry alongside
the command and file surfaces:

```json
{
  "hooks": {
    "PreToolUse": [
      {
        "matcher": "mcp__magus__.*",
        "hooks": [
          { "type": "command", "command": "HOST_EVENT_RAW=1 sh docs/guides/integrations/agents/magus-hook-command.sh", "timeout": 10 }
        ]
      }
    ]
  }
}
```

Same script, same reply shape. An MCP call carries no `tool_input.command`,
only a tool name and a params object, so `HOST_EVENT_RAW=1` forwards the event whole
instead of extracting one field. `magus session hook` already parses that whole envelope;
today it recognizes the tool name and params only well enough to say there is
nothing here it can judge, so this wiring passes every MCP call rather than
denying or advising on one - which is the honest state to ship rather than
silence. The next rule this surface grows reaches the model the moment it
ships, with no new host wiring, because the transport is already here.

## Recording what was read

A third `PreToolUse` entry, matching `Read`, runs
[`magus-hook-observe.sh`](guard-templates.md#magus-hook-observesh). It judges
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
            "command": "sh docs/guides/integrations/agents/magus-hook-observe.sh",
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
orchestrator is handing over in `tool_input.prompt`, the callee's declared
`subagent_type`, and, when the caller named one, `tool_input.model`. The shipped
descriptor includes a matcher for it, so `magus agent harness apply --id
claude-code` installs this entry alongside the surfaces above:

```json
{
  "hooks": {
    "PreToolUse": [
      {
        "matcher": "Agent|Task",
        "hooks": [
          { "type": "command", "command": "HOST_EVENT_RAW=1 sh docs/guides/integrations/agents/magus-hook-command.sh", "timeout": 10 }
        ]
      }
    ]
  }
}
```

Same script as the MCP surface and for the same reason: a spawn's payload is a
prompt, a `subagent_type`, and an optional `model`, not one string, so
`HOST_EVENT_RAW=1` forwards the event whole. `magus session hook` reads
`tool_input.prompt` for the context, `tool_input.subagent_type` (then
`description`, then `tool_name`) for the callee's label, `tool_input.model` for
the model the caller claimed, and `session_id` for the parent's session. The
result is one `agent_spawn` event per lease, with the handed context and the
declared model stored as a payload blob you fetch by ref - `magus session show
<id>` renders each spawn's model claim, wording an absent one as "none
declared" rather than leaving it blank.

The matcher covers both names Claude Code has used for the spawn tool across
releases (`Task` historically, `Agent` currently); a release that emits neither
records nothing here; nothing else in the contract depends on the spelling.

It records; it does not judge. A lease prompt is prose, so the command rules
never run against it and the verdict is always a pass - a prompt that mentions a
denied command describes it rather than runs it, and that holds whether or not
the caller named a model: magus asks the question, it does not grade the
answer. A later decision may add an advisory or a deny keyed on the declared
model; recording it here is what would make that decision possible, not itself
one.

To join those events to a job, write the marker line documented in
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
            "command": "d=$PWD; while [ -n \"$d\" ] && [ ! -f \"$d/magusfile.buzz\" ]; do d=${d%/*}; done; __MAGUS_BIN=$([ -x \"$d/magus\" ] && printf %s \"$d/magus\" || command -v magus 2>/dev/null); [ -n \"$__MAGUS_BIN\" ] && jq -c '{schema_version: 1, outcome: .hook_event_name, source: {kind: \"agent\"}, message: .message}' | \"$__MAGUS_BIN\" session notify --desktop >/dev/null 2>&1; exit 0"
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
If recurring guard evidence crossed its review threshold, it also receives one
improvement-review line with `magus agent improve`; it is a proposal, never an
automatic instruction or memory edit.

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

No transport gap in the guard contract: all three surfaces are wired, `deny`
arrives as a `permissionDecision`, and `advise` arrives as `additionalContext`,
which puts the explanation in front of the model rather than the person. The
MCP surface is transport-complete but rule-empty today - the wiring passes
every call because nothing yet judges an MCP tool name, not because the
channel cannot carry a verdict.

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
fails when it is older than your working tree; **guard wiring** probes it with a
known-denied command and then looks for a host config that invokes a current
template; **agent skills** grades the installed copies against the running binary
and `--fix` reinstalls whatever it reports stale.

Commit `.claude/settings.json` once you are happy with it. Until a checkout has
that file, its guard rules are correct and entirely unenforced, with nothing in
the session saying so - which is the gap the **guard wiring** check exists to
report.
