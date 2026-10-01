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

<!--diagram:agent-surface-->

| what             | where                                                         |
| ---------------- | ------------------------------------------------------------- |
| skills           | `.claude/skills/`                                             |
| guard wiring     | `.claude/settings.json`, `PreToolUse`                         |
| command surface  | deny and advise both reach the model                          |
| file surface     | deny and advise both reach the model                          |
| MCP call surface | deny and advise both reach the model                          |
| read surface     | `PreToolUse` on the read tool: recorded, and judged as `cat`  |
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

Configure MCP for Claude Code yourself. `magus describe harness claude-code`
prints the `claude mcp add` sketch only. Resolve secret ref
`MAGUS_MCP_TOKEN` (env provider by default) for the bearer header; [MCP](../mcp.md)
has the full token setup. Tools are discovered at launch, so restart a client
after changing its MCP configuration. An agent that finds MCP unavailable uses
the CLI fallback; it does not manually start Magus solely to obtain tools.

## Guard hook

Prefer wiring the Claude Code harness from the root magusfile when you bounce
between hosts; `magus describe harness` then covers every wired provider:

```buzz
import "ghcr.io/egladman/magus/spells/harness/claude-code" as claude;
magus\harness.provider(claude);
```

The alias is needed only because `claude-code` is not a Buzz identifier. Declare the
spell in `magus.yaml` with the tag cd's `spell-publish` step pushed, and run your lock
target with `:update` to pin its digest in `magus.lock`, so the harness versions apart
from your magus binary; see [Remote spells](../../../reference/remote-spells.md).

```sh
magus describe harness
magus agent harness verify
```

`magus describe harness` prints each entry `.claude/settings.json` lacks and the one
command that merges them. magus never writes the file: read the command, run it
yourself, then verify.

To adapt that Buzz harness without modifying Magus source: copy the spell into
the workspace, change only the import path (for example
`import "harness/claude-code" as claude`), edit the workspace Buzz, then describe,
merge, and verify again. Details:
[Adapting a Buzz harness](../../../reference/skills/magus-workspace-rules.md) and
[Recurring guard friction](guard.md#recurring-guard-friction).

Or target Claude Code alone:

```sh
magus describe harness claude-code
magus agent harness verify --id claude-code
```

The spell installs entries for commands, file edits, Magus MCP tool calls, reads
(recorded and judged), and sub-agent spawns. Each runs a shipped script that talks to
`magus shell`, through a short launcher that picks which magus runs it. The Bash
entry, as `magus describe harness claude-code` prints it, in the place it lands in
`.claude/settings.json`:

```json
{
  "hooks": {
    "PreToolUse": [
      {
        "matcher": "Bash",
        "hooks": [{
          "type": "command",
          "timeout": 10,
          "command": "m=\"$CLAUDE_PROJECT_DIR/magus\"; [ -x \"$m\" ] || m=$(command -v magus); if [ -z \"$m\" ]; then grep -Fq '\"command\":\"GOEXPERIMENT=jsonv2 go run -trimpath ./cmd/magus run go-build --no-cache .\"' && exit 0; echo 'magus: no ./magus in this checkout and no magus on PATH, so this hook cannot run; build one: GOEXPERIMENT=jsonv2 go run -trimpath ./cmd/magus run go-build --no-cache .' >&2; exit 2; fi; exec \"$m\" buzz -s docs/guides/integrations/agents/magus-command.buzz -- --agent-name claude-code"
        }]
      }
    ]
  }
}
```

This repository's own `.claude/settings.json` holds exactly what the spell prints,
merged with the command `magus describe harness` prints beside it. The scripts are
the glue; the harness only prints the fragments that name them. See
[guard templates](guard-templates.md) for the files and the variables that adapt
them.

Claude Code runs a hook command through `sh -c` and exports `CLAUDE_PROJECT_DIR`,
the root the session started in ([hooks reference](https://code.claude.com/docs/en/hooks)).
So the string is the same on every machine, never an absolute path, and finds the
checkout's own build from any session directory. The glue itself is Buzz and needs
no `jq`; the launcher is the one piece of shell, and it is there so a missing magus
fails closed.

Flags an entry declares ride after `--`, which `magus buzz` forwards to the script,
never in a leading `VAR=value`: the glue reads whether to forward the whole event
off the event itself.

### Which magus runs

Three things in a session resolve the word `magus`, and each has its own owner:

| Who runs `magus` | Resolved by | Owner |
| --- | --- | --- |
| Hook commands (the guard's interpreter) | `$CLAUDE_PROJECT_DIR/magus` when executable, else the first `magus` on PATH | the launcher in every hook entry |
| The agent's own Bash tool commands | PATH, with the session root put first | the `SessionStart` entry (matcher `startup\|resume\|clear`), which appends `export PATH="<root>:$PATH"` to `$CLAUDE_ENV_FILE` |
| Commands magus itself starts (targets, spells, scripts) | PATH, with the running binary's directory put first | magus, on every child it spawns |

The `SessionStart` entry cannot serve hooks: the hooks reference says
`CLAUDE_ENV_FILE` persists variables "for subsequent Bash commands", and names no
other consumer. It is the one entry that runs no magus, since it fixes the PATH a
magus would be found on.

`magus doctor`'s `guard-binary` check runs the interpreter a hook would run and
fails when it is a different build from the doctor's own, or cannot print its
version, naming both binaries.

### When the hook itself cannot run

Wiring the guard to `magus buzz` makes the INTERPRETER a magus, where the sh
copy's interpreter was `/bin/sh`: always present, and with no version to be wrong.
So a magus that is missing, too old to run the script, or unable to load this
workspace does not merely answer badly; it never runs the script at all.

Claude Code treats a hook that exits non-zero with any code other than 2 as a
[non-blocking error](https://code.claude.com/docs/en/hooks): the first line of its
stderr appears in the transcript as a `<hook name> hook error` notice, and the tool
call goes ahead UNJUDGED. A bare `magus` with none on PATH exits 127 that way, and
a whole session once ran with every guard rule open.

So when no magus resolves, every `PreToolUse` entry exits 2: the call is refused,
and the reason names the command that builds one. The one call let through is that
command, `GOEXPERIMENT=jsonv2 go run -trimpath ./cmd/magus run go-build --no-cache .`, exactly and alone,
because a fresh checkout has no other way to get its first binary; the guard exempts
the same line once a binary exists to run it. Entries after the call (`PostToolUse`,
`Stop`, `SubagentStop`, `SessionStart`) exit 1 and only report: exit 2 on `Stop`
would keep the agent from ever stopping. The `SessionStart` entry says the same at
the start of a session, before the first refusal.

A magus that resolves but is broken (too old, or unable to load the workspace)
still fails open, with its own error as the notice. `magus doctor` and `magus agent
harness verify --id claude-code` report that before a session starts; verify runs
each wired command against a synthetic event and checks the verdict comes back. The
glue never blocks on its own failure either: a verdict it cannot obtain is reported
and the call proceeds.

A push at a commit no passing gate covers gets the verdict `ask`, and the command
template renders it as `permissionDecision: "ask"`: Claude Code shows you the
reason, which names the commit and the gate state, and approving publishes it. A
push the gate covers runs without a prompt. A session bound to a job lease is
denied instead, because workers do not publish.

### Maintaining the workspace harness

This host is a Buzz harness spell. Adapt without Magus source edits by forking
the spell and changing only the import path; then merge what `magus describe
harness` prints and run `magus agent harness verify`. See
[Adapting a Buzz harness](../../../reference/skills/magus-workspace-rules.md) and
[Recurring guard friction](guard.md#recurring-guard-friction).

## MCP tool calls

Claude Code's `PreToolUse` also fires for a tool served over MCP, matching
`mcp__<server>__<tool>`. The shipped spell includes a Magus-MCP matcher,
so `magus describe harness claude-code` prints this entry alongside the command
and file surfaces:

```json
{
  "hooks": {
    "PreToolUse": [
      {
        "matcher": "mcp__magus__.*",
        "hooks": [
          { "type": "command", "command": "./magus buzz -s docs/guides/integrations/agents/magus-command.buzz", "timeout": 10 }
        ]
      }
    ]
  }
}
```

Same script, same command string, same reply shape. An MCP call carries no
`tool_input.command`, only a tool name and a params object, and that absence is what
the glue reads: with nothing at `HOST_EVENT_PATH` to select, it forwards the event
whole instead of extracting one field, so the entry says nothing the event does not
already say. `magus session hook` already parses that whole envelope;
today it recognizes the tool name and params only well enough to say there is
nothing here it can judge, so this wiring passes every MCP call rather than
denying or advising on one - which is the honest state to ship rather than
silence. The next rule this surface grows reaches the model the moment it
ships, with no new host wiring, because the transport is already here.

## Recording and judging what was read

Two `PreToolUse` entries match `Read`. The first runs
[`magus-observe.buzz`](guard-templates.md#magus-observebuzz). It judges
nothing and prints nothing: it records the path on the activity trail so a later
`magus session show` can say what a session looked at, not only what it changed.

The second runs `magus-command.buzz`, which restates the read as the shell line
it stands for: `cat <file>`, or `sed -n <first>,<last>p <file>` over the call's
offset and limit, a limit over 300 counting as whole. The read rules then judge
it as they judge that `cat`: a whole read of a Go, Buzz or Markdown file over
120 lines is refused with the file's map, and a bounded read inside one indexed
declaration is advised toward `magus refs`.

```json
{
  "hooks": {
    "PreToolUse": [
      {
        "matcher": "Read",
        "hooks": [
          {
            "type": "command",
            "command": "./magus buzz -s docs/guides/integrations/agents/magus-observe.buzz",
            "timeout": 10
          }
        ]
      },
      {
        "matcher": "Read",
        "hooks": [
          {
            "type": "command",
            "command": "./magus buzz -s docs/guides/integrations/agents/magus-command.buzz",
            "timeout": 10
          }
        ]
      }
    ]
  }
}
```

The observer is the one job here that is deliberately not carried to every host:
it changes no verdict, so a reader who skips it loses detail in a trail rather
than enforcement. The judging entry is enforcement, and Codex carries it too.

## Lease capture

When Claude Code hands work to a sub-agent it does so through a tool call, and
that call fires `PreToolUse` like any other - carrying the whole prompt the
orchestrator is handing over in `tool_input.prompt`, the callee's declared
`subagent_type`, and, when the caller named one, `tool_input.model`. The shipped
descriptor includes a matcher for it, so `magus describe harness claude-code`
prints this entry alongside the surfaces above:

```json
{
  "hooks": {
    "PreToolUse": [
      {
        "matcher": "Agent|Task|SendMessage",
        "hooks": [
          { "type": "command", "command": "./magus buzz -s docs/guides/integrations/agents/magus-command.buzz -- --observes-skill-loads", "timeout": 10 }
        ]
      }
    ],
    "PostToolUse": [
      {
        "matcher": "Agent|Task",
        "hooks": [
          { "type": "command", "command": "./magus buzz -s docs/guides/integrations/agents/magus-command.buzz", "timeout": 10 }
        ]
      }
    ],
    "SubagentStop": [
      {
        "hooks": [
          { "type": "command", "command": "./magus buzz -s docs/guides/integrations/agents/magus-command.buzz", "timeout": 10 }
        ]
      }
    ]
  }
}
```

`SendMessage` is the continuation of a subagent that already exists: a
`tool_input` carrying `to` and `message`. It reaches the same verdict path as a
spawn, so a workspace [`magus\guard.spawn`](../../../reference/guard-spawn.md)
rule sees both. The `PostToolUse` entry judges nothing: the finished call's
`tool_response.agentId` is the id the host gave the child, and recording it
against the spawn's `description` is what lets that child's own spawns name
their parent (`agent_id` on its later hook events). A live background spawn
carries that response field; a release that drops it leaves every parent empty
rather than guessed. The same record keeps the model the spawn
named, and, when the spawn's `description` reads `<parent>/<role> <job>` for a
live job, the job its later calls are graded under, in its own worktree too.

The `SubagentStop` entry judges nothing either. Its payload names the finished
subagent's `agent_id` and `agent_transcript_path`; magus reads the last usage
record in that transcript's final 512 KiB and files input plus cache-read plus
cache-write tokens as the agent's context size, which a later `SendMessage` to it
hands a [`magus\guard.spawn`](../../../reference/guard-spawn.md) rule as
`target.contextTokens`.

Same script as the MCP surface and for the same reason: a spawn's payload is a
prompt, a `subagent_type`, and an optional `model`, not one string, so there is no
`tool_input.command` to select and the event goes whole. The one thing written on
this entry is the flag after `--`, which `magus buzz` forwards to the script as its
own argv: `--observes-skill-loads` says that THIS config also matches the host's
`Skill` tool, so a rule that requires a skill before a spawn has loads to read. A
config without that matcher omits the flag and those rules stand down rather than
denying every spawn forever. The script parses that tail against the flags it
supports; an argument it does not know is named on stderr, which Claude Code shows
as a hook error, and the call is judged without it rather than blocked.
`magus session hook` reads
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
send an event to the server or Console. Wire `Notification` (it fires on a
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

Wire `Stop` to [`magus-checkpoint.buzz`](guard-templates.md#magus-checkpointbuzz)
and each time a turn ends magus records the revision, branch and dirtiness of the
tree, plus this session's id and transcript path. `magus session` lists it.

```json
{
  "hooks": {
    "Stop": [
      {
        "hooks": [
          {
            "type": "command",
            "command": "./magus buzz -s docs/guides/integrations/agents/magus-checkpoint.buzz",
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
[`magus-rehydrate.buzz`](guard-templates.md#magus-rehydratebuzz) and a session that
just lost its history is handed this checkout instead: branch and revision,
commits not yet on the base ref, the dirty tree split into sources, generated
outputs and unclaimed paths, the live leases with the command that binds each
one, the last recorded run's failures with the ref that holds their output, the
guard wiring, and where the rules live.
If recurring guard evidence crossed its review threshold, it also receives one
line pointing to `magus doctor`'s recurring-guard-denials check; it states facts,
never an automatic instruction or memory edit.

```json
{
  "hooks": {
    "SessionStart": [
      {
        "matcher": "compact|resume",
        "hooks": [
          {
            "type": "command",
            "command": "./magus buzz -s docs/guides/integrations/agents/magus-rehydrate.buzz",
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
rules live in, `CLAUDE.md` by default and `-- --rules <file>` when yours is
somewhere else. Run `magus session --brief` yourself to see what a session will
be handed.

## Coverage and limits

No transport gap in the guard contract: all three surfaces are wired, `deny`
arrives as a `permissionDecision`, and `advise` arrives as `additionalContext`,
which puts the explanation in front of the model rather than the person. The
MCP surface is transport-complete but rule-empty today - the wiring passes
every call because nothing yet judges an MCP tool name, not because the
channel cannot carry a verdict.

**Shell commands run with stdin at end-of-file.** An agent's command inherits a
stdin nobody writes to, so a stray reader (grep with no file operand, `read`, a
prompt, ssh, a pager) waits forever, and Claude Code backgrounds a timed-out
command rather than killing it. On a pass or an advise, the reply hands the
command back as `updatedInput`, prefixed `exec </dev/null;`, with every other
`tool_input` field kept, since `updatedInput` replaces the whole input. It sets no
`permissionDecision`, so the rewritten call still meets your permission rules. A
heredoc, a pipe or a `<` still feed their command. A denied or asked call is never
rewritten, a command that already starts with the prefix is left alone, the
session is told once, and the activity trail records `stdin_closed` on the
verdict.

A worktree-isolated subagent meets one side effect: Claude Code's isolation check
judges the rewritten line, and refuses the prefix on a line it already finds
borderline (runtime-computed values beside a redirect) as "exec inside a construct
too complex to verify". Split such a line, as that refusal says. A
`{ <command>` ... `} </dev/null` group avoids the word but was refused far more
often, even around `stat` or `git status`.

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
