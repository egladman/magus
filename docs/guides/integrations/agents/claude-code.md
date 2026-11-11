---
title: Claude Code
description: Wiring magus into Claude Code (skills in .claude/skills, the two PreToolUse guard hooks, attention notifications, and the checks that prove the guard is running).
tags: [agents, claude, claude code, skills, guard, hooks, notifications]
---

# Claude Code

Claude Code reads Agent Skills from `.claude/skills/` and runs a `PreToolUse`
hook before every tool call. That covers the shell-command and file-write rules, and both verdicts
reach the model, so nothing in the contract is lost here. It is also the setup
this repository dogfoods and the only one executed end to end against a real
event.

<!--diagram:agent-integration-->

| what             | where                                                         |
| ---------------- | ------------------------------------------------------------- |
| skills           | `.claude/skills/`                                             |
| guard wiring     | `.claude/settings.json`, `PreToolUse`                         |
| shell commands   | deny and advise both reach the model                          |
| file writes      | deny and advise both reach the model                          |
| MCP calls        | deny and advise both reach the model                          |
| file reads       | `PreToolUse` on the read tool: recorded, and judged as `cat`  |
| MCP              | [MCP](../mcp.md)                                              |
| attention events | `Notification`, `Stop`, `SubagentStop`                        |
| checkpoint       | `Stop`                                                        |
| reply check      | opt-in: `Stop` judges, `UserPromptSubmit` hands it over       |
| your messages    | `UserPromptSubmit`, recorded and never answered               |
| rehydration      | `SessionStart` (`compact`, `resume`)                          |
| lease            | `PreToolUse` on the sub-agent tool                            |
| declared model   | `PreToolUse` on the sub-agent tool, when the caller named one |
| quiet output     | `env.MAGUS_LOG_SILENT` in `.claude/settings.json`: `true`     |

## Skills

```sh
magus agent install .claude/skills
```

Commit what it writes so every teammate's agent gets the same instructions.
Claude Code discovers skills when a session starts, so restart the session
before it can invoke anything new. [Skills](skills.md) covers the install,
the two forms, and the drift check.

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
import "magus";
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

The spell wires every tool call to the guard through one `PreToolUse` entry, records
reads, finished spawns and stops, and records the person's messages without answering
them. Each entry is one `magus buzz` command that runs a shipped script, which talks
to `magus shell`: reads are also recorded by `magus-observe.buzz`, and every other
entry runs `magus-command.buzz`. The catch-all entry, as `magus describe harness
claude-code` prints it, in the place it lands in `.claude/settings.json`:

```json
{
  "hooks": {
    "PreToolUse": [
      {
        "matcher": "*",
        "hooks": [{
          "type": "command",
          "timeout": 10,
          "command": "m=\"$CLAUDE_PROJECT_DIR\"/magus; [ -x \"$m\" ] || m=magus; \"$m\" buzz -C \"$CLAUDE_PROJECT_DIR\" -s docs/guides/integrations/agents/magus-command.buzz -- --agent-name claude-code"
        }]
      }
    ]
  }
}
```

The glue forwards the whole event, and magus judges the call by its tool and the shape
of its input: a command line, a file read or write, a search, a spawn, a skill load,
an MCP call. The spell's tool rows say what each Claude Code tool does where the shape
cannot; `magus agent harness install` writes them to `.claude/magus/tools`, one file per
capability with one tool name per line, beside a host file naming claude-code, where magus
finds them by the entry's `--agent-name`. A tool the rows list as touching nothing (a todo list, a plan, a web lookup)
passes before any rule loads; a tool whose input has none of those shapes and that no row
lists is named by the unjudged-tool rule rather than run unseen. See
[Every tool reaches the guard](guard.md#every-tool-reaches-the-guard).

The catch-all replaced the per-tool entries, among them the `Edit|Write|NotebookEdit`
entry that ran `magus-path.buzz`. A `.claude/settings.json` merged before that still
carries them, and `magus describe harness claude-code` lists each as one to retire.

This repository's bootstrap installs `.claude/settings.json` from the canonical
harness spell, merging managed hooks with existing local settings. The installed
file is ignored; the harness sources remain reviewed in version control. See
[guard templates](guard-templates.md) for the files and the variables that adapt
them.

Claude Code runs a hook command through `sh -c` and exports `CLAUDE_PROJECT_DIR`,
the root the session started in ([hooks reference](https://code.claude.com/docs/en/hooks)).
So the string is the same on every machine, never an absolute path. `-C` makes the
root the working directory before anything else, so the root-relative script path
resolves wherever the session is; the glue reads the session's own directory off
the event. The glue is Buzz and needs no `jq`. The only shell an entry carries is
the binary choice before `buzz`, POSIX `sh` with no pipe or substitution.

Flags an entry declares ride after `--`, which `magus buzz` forwards to the script,
never in a leading `VAR=value`: the glue reads whether to forward the whole event
off the event itself.

### Which magus runs

Three things in a session resolve the word `magus`, and each has its own owner:

| Who runs `magus`                                        | Resolved by                                                               | Owner                                                                                                                                              |
| ------------------------------------------------------- | ------------------------------------------------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------- |
| Hook commands (the guard's interpreter)                 | `$CLAUDE_PROJECT_DIR/magus` when executable, else PATH                    | every hook entry, in the shell before `buzz`                                                                                                       |
| The binary that judges a call                           | `./magus` beside the nearest `magus.yaml` above the call's cwd, else PATH | `magus-command.buzz` and the other glue, from the event's `cwd`                                                                                    |
| The agent's own Bash tool commands                      | PATH, with the session root put first                                     | the `SessionStart` entry (matcher `startup\|resume\|clear`), whose `magus-session.buzz` appends `export PATH="<root>:$PATH"` to `$CLAUDE_ENV_FILE` |
| Commands magus itself starts (targets, spells, scripts) | PATH, with the running binary's directory put first                       | magus, on every child it spawns                                                                                                                    |

A hook entry runs the session root's `./magus` when it is executable, and the magus on
the PATH Claude Code was started with otherwise. That binary only interprets the glue.
The glue reads the event's `cwd`, walks up to that checkout's workspace root, and
judges the call with the `./magus` there; only a checkout with none falls back to PATH.
The shell cannot read the event, so it cannot pick the cwd's binary itself, and magus
does not re-exec the guard into another binary. When a linked checkout has no `./magus`
while the session root does, the glue says which PATH binary judged instead. The
`SessionStart` entry cannot serve hooks: the hooks reference says `CLAUDE_ENV_FILE`
persists variables "for subsequent Bash commands", and names no other consumer.

The workspace root is the nearest directory holding `magus.yaml`, the file
`magus.FindRoot` also stops at, and not the nearest `magusfile.buzz`: `console/`,
`docs/` and each `libs/*` project carry a magusfile and no binary, so a call made from
one of them would otherwise have fallen to the PATH binary. A worktree under
`.claude/worktrees/` holds its own `magus.yaml`, so the walk stops there and never
climbs into the parent checkout. A tree with no `magus.yaml` above falls back to the
nearest `magusfile.buzz`.

`-C` names the main checkout even for a session in a worktree, so the glue reads the
directory the call runs in from the event and resolves the binary from there. The
`SessionStart` entry does the same: it puts the root above the event's `cwd` on PATH,
not the one `-C` names.

A subagent's `cd` never persists between Bash calls, as Claude Code documents
for the Bash tool
([Bash tool behavior](https://code.claude.com/docs/en/tools-reference#bash-tool-behavior)).
A worker in a linked checkout therefore names it on every magus line with `-C <root>`
or a `cd` in the same command, and the guard's `lease-checkout` rule judges the call
where that line puts it.

`magus doctor`'s `guard-binary` check runs the interpreter a hook would run, from the
root and from every project directory, and fails when one is a different build from the
doctor's own, cannot print its version, or does not exist. It also fails a linked
worktree that holds no `./magus`, since its hooks then judge with whatever magus is on
PATH, which may not load the tree. The trail records the judging binary's path and
version and the event's `cwd` on every hook row.

### Quiet output

`MAGUS_LOG_SILENT=true` is `-s` on every magus command: verdicts, refs and failures,
without each reason or a wait note under a minute. The printed settings set it in
Claude Code's own `env` object, which Claude Code sets on the session, so every Bash
tool command inherits it:

```json
{ "env": { "MAGUS_LOG_SILENT": "true" } }
```

`magus describe harness claude-code` sets only the variable your `env` lacks, beside
whatever else it holds. A value you already chose, `false` included, stays.

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

So with no magus on PATH every hook runs open: each exits 127, Claude Code shows
the notice, and the call goes ahead unjudged. Start Claude Code from a shell whose
PATH finds one. This repository's `mise.toml` puts the checkout root first on PATH,
so an activated shell finds the checkout's own `./magus` once
`GOEXPERIMENT=jsonv2 go run -trimpath ./cmd/magus run go-build --no-cache .` has
built it.

A magus that is present but too old to run `magus shell` at all fails open the same
way, with its own error as the notice. `magus doctor` and `magus agent harness verify
--id claude-code` report that before a session starts; verify runs each wired command
against a synthetic event and checks the verdict comes back. The glue never blocks on a
failure of its own: a verdict it cannot obtain is reported and the call proceeds.

A magus that runs but cannot load the workspace is different, because the verdict it
renders comes from the built-in rules alone and the workspace's own rules judged
nothing. When neither the working tree nor its approved copy loads, and the binary is
older than the tree (a name the magusfile calls that this build predates, such as
`magus\guard.builtins`) or the checkout holds no `./magus`, the guard denies every call
that changes state under the `stale-binary` rule: file edits, spawns, pushes, every shell
command that changes state, and the magus MCP tools that write: `client`, which runs a
script, and `diff` with an `op` other than `state` or `thread`. The tools that only read
(`status`, `config`, `console`, `buzz`, `diff` with no `op`, `op=state` or `op=thread`)
still answer. Reads, `git status`, `magus affected --explain` and the fix still run, and
so do a read-only scout's `magus job exec`, `magus job exit` and `magus buzz --record`,
and the lines that get a tree left mid-pick back to one a binary can be built from
(`git cherry-pick`, `git merge`, `git rebase` or `git revert` with `--abort` or
`--continue`, and `git worktree add`). While a magusfile import names a file that is not
there, putting that file back also runs, with an advisory that says why: `git mv` onto
it, `git restore` or `git checkout --` of it, or a write to it; the bootstrap the denial
prints runs for that failure too. The denial names the cause and the one likeliest fix; the fallbacks sit behind its `full
verdict:` ref. The workspace needs only to show a guard rule in its magusfile for this,
so a fresh worktree is covered before any policy has loaded in its cache.

The fix depends on who is asking. The checkout's integrator (a job whose check is the
gate) or a person rebuilds with `./magus run go-build .`, or bootstraps with the command
the denial prints where the checkout has no binary. An orchestrating main session never
builds, and a worker holding a lease never builds one: there is one binary per base, and
the integrator builds it and a copy is placed in the worker's checkout with
`hack/dev/bootstrap-worktree.buzz`, so the denial tells the worker to ask for that. A
worktree session with no `./magus` is an error, where it used to run open on whatever
PATH held; `SessionStart` says so once per session, naming one command, whenever the
binary that would judge the session cannot load the tree.

A run of a `human_only` target gets the verdict `ask`. Under `permission_mode`
`default`, `acceptEdits` or `auto`, the command template renders it as
`permissionDecision: "ask"`: Claude Code shows you the reason, and approving runs it.
Any other mode, or none, may never prompt, so the template refuses the call with
`permissionDecision: "deny"`. Its `systemMessage` shows you what happened, why this
session cannot ask, and the command to run in your own terminal. The model reads the
same, with word to relay it verbatim if you did not see it.

A push at a commit no passing gate covers is denied, and the reason names the commit
and the gate state. A push the gate covers runs without a prompt. A session bound to
a job lease is denied both, because workers do not publish.

### Maintaining the workspace harness

This host is a Buzz harness spell. Adapt without Magus source edits by forking
the spell and changing only the import path; then merge what `magus describe
harness` prints and run `magus agent harness verify`. See
[Adapting a Buzz harness](../../../reference/skills/magus-workspace-rules.md) and
[Recurring guard friction](guard.md#recurring-guard-friction).

### Checking the Buzz it writes

`PostToolUse` on `Write|Edit|MultiEdit` runs
[`magus-buzz-check.buzz`](guard-templates.md#magus-buzz-checkbuzz), which runs
`magus buzz --check` on a `.buzz` file the agent just wrote and hands a failure back
as the reason to fix it:

```json
{
  "hooks": {
    "PostToolUse": [
      {
        "matcher": "Write|Edit|MultiEdit",
        "hooks": [
          {
            "type": "command",
            "command": "m=\"$CLAUDE_PROJECT_DIR\"/magus; [ -x \"$m\" ] || m=magus; \"$m\" buzz -C \"$CLAUDE_PROJECT_DIR\" -s docs/guides/integrations/agents/magus-buzz-check.buzz -- --agent-name claude-code",
            "timeout": 30
          }
        ]
      }
    ]
  }
}
```

## MCP tool calls

Claude Code's `PreToolUse` also fires for a tool served over MCP, named
`mcp__<server>__<tool>`, and the catch-all entry receives it like any other call. A
call to one of magus's own tools (`mcp__magus__*`) is judged as the CLI verb it
stands for, so a rule that holds on one channel holds on the other. A call to
another server's tool carries a params object magus cannot read, so the unjudged-tool
rule names it: it advises by default, and a workspace that wants every call judged
sets it to deny.

## Recording and judging what was read

One `PreToolUse` entry matches `Read` alone. It runs
[`magus-observe.buzz`](guard-templates.md#magus-observebuzz), which judges
nothing and prints nothing: it records the path on the activity trail so a later
`magus session show` can say what a session looked at, not only what it changed.

The catch-all entry judges the same read. magus restates it as the shell line it
stands for: `cat <file>`, or `sed -n <first>,<last>p <file>` over the call's offset
and limit, a limit over 300 counting as whole. The read rules then judge it as they
judge that `cat`, whichever tool performs it: a whole read of a Go, Buzz or Markdown
file over 120 lines is refused with the file's map, a bounded read inside one indexed
declaration is advised toward `magus refs`, and a third window paged through one file
is refused whether the windows came from `sed -n` or from `Read`.

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
      }
    ]
  }
}
```

The observer is the one job here that is deliberately not carried to every host:
it changes no verdict, so a reader who skips it loses detail in a trail rather
than enforcement. The judging entry is enforcement, and Codex carries it too.

## Lease capture

When Claude Code hands work to a subagent it does so through a tool call, and
that call fires `PreToolUse` like any other, carrying the whole prompt the
orchestrator is handing over in `tool_input.prompt`, the callee's declared
`subagent_type`, and, when the caller named one, `tool_input.model`. The catch-all
entry receives it before it runs, and two more entries see it finish:

```json
{
  "hooks": {
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

The tools that wait reach the catch-all entry too: `Monitor`, `TaskOutput`,
`ScheduleWakeup` and `CronCreate`. A `Monitor` runs its `command` in
the background and wakes the agent on each line it prints, so the command is judged
as the shell line it is (busy-wait and process-poll apply), and the waiter rules
count the watchers each agent holds: a second live one on the same subject, or one
on work the agent backgrounded itself, is a duplicate, and five live at once are a
pile (see [the guard](guard.md#what-magus-denies-with-a-served-command)). `ScheduleWakeup`
and `CronCreate` carry a `prompt` beside a delay or a cron expression, which magus
reads as a prompt queued for the same session rather than a spawn.

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

The `SubagentStop` entry also measures the subagent. Its payload names the finished
subagent's `agent_id` and `agent_transcript_path`; magus reads the last usage
record in that transcript's final 512 KiB and files input plus cache-read plus
cache-write tokens as the agent's context size, which a later `SendMessage` to it
hands a [`magus\guard.spawn`](../../../reference/guard-spawn.md) rule as
`target.contextTokens`.

A spawn's payload is a prompt, a `subagent_type`, and an optional `model`, not one
string, and the event goes whole as every call's does. Whether a config sees skill loads
is the spell's to say: `mgs_getLimits` returns `observesSkillLoads`, install writes it
beside the tool rows, and the catch-all matcher meets the `Skill` tool, so a rule that
requires a skill before a spawn, a write or a command has loads to read. A load counts for
the agent that made it: a subagent loads a skill itself, and its parent's load does not
count for it. A host whose wiring sees no skill load says so in its limits, and those
rules stand down rather than denying forever. The script takes only `--agent-name`; any
other argument is named on stderr, which Claude Code shows
as a hook error, and the call is judged without it rather than blocked.
`magus session hook` reads
`tool_input.prompt` for the context, `tool_input.subagent_type` (then
`description`, then `tool_name`) for the callee's label, `tool_input.model` for
the model the caller claimed, and `session_id` for the parent's session. The
result is one `agent_spawn` event per lease, with the handed context and the
declared model stored as a payload blob you fetch by ref: `magus session show
<id>` renders each spawn's model claim, wording an absent one as "none
declared" rather than leaving it blank.

The `PostToolUse` matcher covers both names Claude Code has used for the spawn tool
across releases (`Task` historically, `Agent` currently); a finished spawn under any
other name records no child id.

It records; it does not judge. A lease prompt is prose, so the command rules
never run against it and the verdict is always a pass: a prompt that mentions a
denied command describes it rather than runs it, and that holds whether or not
the caller named a model: magus asks the question, it does not grade the
answer. A later decision may add an advisory or a deny keyed on the declared
model; recording it here is what would make that decision possible, not itself
one.

To join those events to a job, write the marker line documented in
[Any other host](any-host.md#lease-capture) at the top of the prompt you
hand the subagent.

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
            "command": "d=$PWD; while [ -n \"$d\" ] && [ ! -f \"$d/magusfile.buzz\" ]; do d=${d%/*}; done; __MAGUS_BIN=$([ -x \"$d/magus\" ] && printf %s \"$d/magus\" || command -v magus 2>/dev/null); [ -n \"$__MAGUS_BIN\" ] && jq -c '{api_version: \"magus.event/v1alpha1\", outcome: .hook_event_name, source: {kind: \"agent\"}, message: .message}' | \"$__MAGUS_BIN\" session notify --desktop >/dev/null 2>&1; exit 0"
          }
        ]
      }
    ]
  }
}
```

It exits 0 and swallows its own output on purpose: a notifier that can fail is a
hook that can break the session it was meant to watch. It opens with the same
workspace-root walk as the lease hook above, for the same reason.
[Attention hooks](notifications.md) covers the envelope and the outcome vocabulary.

## Recording how long a tool ran

Wire `PostToolUse` to
[`magus-duration.buzz`](guard-templates.md#magus-durationbuzz) (this repository
does, under the matcher that excludes `Agent|Task`) and each finished call
records how long the host said the tool took. It judges nothing and exits 0.

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

## Watching free disk space

Wire `UserPromptSubmit` to
[`magus-reclaim-disk.buzz`](guard-templates.md#magus-reclaim-diskbuzz) and each
turn costs one stat of the free-space store until a minute has passed. Past that
it says what the store holds that nobody was told, and starts one detached tick
that trims caches when free space runs low.

```json
{
  "hooks": {
    "UserPromptSubmit": [
      {
        "hooks": [
          {
            "type": "command",
            "command": "./magus buzz -s docs/guides/integrations/agents/magus-reclaim-disk.buzz -- --agent-name claude-code",
            "timeout": 10
          }
        ]
      }
    ]
  }
}
```

## Judging the reply a turn ends with

The spell does not wire this. The reply check judges your agent's replies against the
rules in `proofread`, which is a choice about how your agent writes and not part of the
guard, so a merge never writes it and never removes one you add. To turn it on, download
[`proofread-reply.buzz`](guard-templates.md#proofread-replybuzz) and add it under both
`Stop` and `UserPromptSubmit` in `.claude/settings.json` by hand. At `Stop` the script
runs the `proofread` binary over the turn's last message and prints nothing. Context a
`Stop` hook prints starts a turn, and you would see the agent answer advice you never saw.
It keeps up to five findings per session in `$XDG_STATE_HOME/magus/proofread-reply/` and
hands them to the agent as `additionalContext` with your next message. It needs no magus workspace, only `proofread` on `PATH`, in
`__PROOFREAD_BIN`, or named with `-- --proofread PATH`; `__PROOFREAD_DECISIONS` or
`-- --decisions FILE` sets your own decisions. It never blocks the stop and prints
nothing when proofread is missing or the agent is already continuing.
[Check writing in a commit hook](../../proofread.md) covers the setup.

```json
{
  "hooks": {
    "Stop": [
      {
        "hooks": [
          {
            "type": "command",
            "command": "magus buzz -C \"$CLAUDE_PROJECT_DIR\" -s docs/guides/integrations/agents/proofread-reply.buzz -- --agent-name claude-code",
            "timeout": 10
          }
        ]
      }
    ],
    "UserPromptSubmit": [
      {
        "hooks": [
          {
            "type": "command",
            "command": "magus buzz -C \"$CLAUDE_PROJECT_DIR\" -s docs/guides/integrations/agents/proofread-reply.buzz -- --agent-name claude-code",
            "timeout": 10
          }
        ]
      }
    ]
  }
}
```

`--agent-name` is required: the script picks the output shape of its host from it, and
Codex and Cursor take different ones.

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
somewhere else. Run `magus session --brief` yourself to see what a session
is handed.

## The magus mod

The repository is a Claude Code plugin marketplace with one mod in it, `magus`,
whose source is `docs/guides/integrations/agents/claude-code/mod/`. Add the marketplace once per person:

```text
claude plugin marketplace add egladman/magus
```

From a local checkout, `claude plugin marketplace add <checkout> --scope local` then
`claude plugin install magus@magus --scope local` does the same.

It adds these to a session:

- A status line entry that names a magus problem: no binary, a stopped daemon, a
  daemon running another version, or an MCP endpoint that is not serving. The band
  above the prompt draws only the two that nothing else says, a guard that fails
  open and a daemon on another version, with a Copy fix button that says when the
  copy did not reach a clipboard. A stale or borrowed `./magus` is not drawn: the
  guard names the rebuild to the agent.
- A pane, opened with `/magus`, of the job tree, recent runs, and the session's
  subagents. It reads the daemon's Connect API over its unix socket, the same
  services the console uses, so it needs `magus server start` and holds no token.
  A subagent spawned as `<parent>/<role> <job>` with role `orchestrator`,
  `integrator` or `worker` draws on a muted PatternFly purple, teal or blue, the
  10 step on a light theme and the 80 step on a dark one. Under a theme whose
  name gives no shade, such as `auto`, the rows stay untinted and the tab says so.
  Claude Code's own task list cannot be styled by a mod, so only this pane is tinted.
- A warning or a held prompt when you submit after the prompt cache has expired,
  priced from a rates table you can replace through the mod's `ratesFile` setting.
  There is no countdown: the moment that matters is the submit.
- Cost gates, each priced from the same table. The cache and price step gates are
  `off`, `warn` (a toast) or `confirm`, `confirm` by default. Each gate speaks once per response, so a second
  submit before the next response, queued mid-turn or sent after a hold, goes
  through without a word. The price step gate acts
  when the context the next prompt re-sends is at or within `priceStepMarginTokens`
  of a model's prompt-size price step, past which every request costs more, and
  `confirm` holds the prompt once and suggests `/compact`. The subagent gate is `off`
  or `deny`, `off` by default: `deny` checks the model a spawn runs on against
  `spawnMaxInputUsdPerMTok` (2 by default, Sonnet's input rate; 0 turns it off) and
  refuses the spawn before it starts, pointing to the read-only `magus-scout` agent
  or `model: haiku`. The budget gate acts once the session's spend passes
  `sessionBudgetUsd` (0 by default, which is off), and holds the prompt once by
  default (`budgetGate`: `off`, `warn` or `confirm`). A subagent's token count is shown, never flagged: context size is not billed
  cost.
- What the session and the day have cost, on the status line:
  `≈$1.75 session · ≈$12.40 today`. The session figure is Claude Code's own
  total, the one `/cost` shows; the day adds each session's growth to a total in
  the mod's store, so a session past midnight splits across both days. Where
  Claude Code reports no total, the mod's sum from its rates table stands in,
  marked `(mod rates)`. Every figure is an estimate at list price, never the bill.
  The pane shows the same total with the mod's split by main conversation and
  subagents.
- The machine build budget, read from `magus server status -o json` every 15 seconds and
  around each Bash call that runs magus: each holder's pid, target, slots, memory,
  age and checkout, the callers waiting for a seat, and the
  [MGS3009](../../../reference/codes/sandbox/MGS3009.md) refusals, on the pane's
  Budget tab. Nothing about it reaches the status line or the band: an agent reads
  a refusal and retries.
- A table under each reply that names a `.jsonl` file, as a link, a code span or a
  bare path, and under each `SendUserFile` call that sends one: one column per key
  in the order the file first names it, rows in file order, nested values as
  one-line JSON, and a red `✖ line N:` for each line that is not a JSON object.
  A named file that is not there draws one dim line. A cell past 160 characters,
  and the rows past 200, are cut, and the table says how many rows it left out.
- A line of links under each reply and each finished magus call, Bash or MCP, that
  names an output ref, a guard detail or an invocation id. A link's title is the
  command that prints it. Pressing one opens a pane with that command's output and a
  Copy command button. Where the host opens links itself and no press reaches the
  mod, the link opens the page on [output refs](../../../concepts/cache/output-refs.md).

Every threshold is a setting in `/config`: `cacheGate`, `gateMinTokens`, `cacheTtl`,
`priceStepGate`, `priceStepMarginTokens`, `spawnGate`, `spawnMaxInputUsdPerMTok`,
`sessionBudgetUsd`, `budgetGate` and `ratesFile`. A `spawnGate` of `warn`, and the
settings `cacheWarnMinutes`, `subagentTokenBudget` and `ratesStaleDays`, are gone.
The mod reads a spawn's model from the call, then
the agent's definition file under `.claude/agents`, then
`CLAUDE_CODE_SUBAGENT_MODEL`, then the session; a built-in or plugin agent it cannot
read is judged after it starts, by a toast alone in `deny` mode. The mod uses the early-access Claude
Code plugin API, so a Claude Code update can change what it needs.

A workspace can register the marketplace and turn the mod on for everyone who opens
it, through an opt-in harness beside the one that wires the guard:

```buzz
import "spells/harness/claude-code-mod" as claudeMod;
magus\harness.provider(claudeMod);
```

`magus describe harness claude-code-mod` then prints the two settings it keeps in
`.claude/settings.json`, `extraKnownMarketplaces.magus` and
`enabledPlugins["magus@magus"]`, with the command that merges them. Claude Code
reads the marketplace only once the person trusts the folder. The settings do not
install the mod: each person runs the line above once, or trusts the folder and
starts a session, and until then the mod never appears. `magus agent harness verify
claude-code-mod` and `magus doctor` read what the host records, in the `plugins`
directory of `CLAUDE_CONFIG_DIR` (`~/.claude` by default) or in
`CLAUDE_CODE_PLUGIN_CACHE_DIR` when that is set. The mod counts as installed only
when `installed_plugins.json` lists `magus@magus` in the user scope, or in a local
or project scope for this checkout; a cached plugin directory alone no longer
counts. An install that `.claude/settings.local.json` turns off reads as disabled,
with the `claude plugin enable` command. An installed mod is current when its
recorded commit is the HEAD of the marketplace clone `known_marketplaces.json`
names; an older commit reads as behind, with the `claude plugin update` command,
and a record that cannot prove either reads as unknown. When the host has never
run here (no config directory), the check is skipped. Verify fails while the
workspace enables the mod and it is not installed; doctor advises on that gap so
CI is not red for a personal install. A workspace that does not enable the mod is
not checked.

## Coverage and limits

No transport gap in the guard contract: all three kinds of input are wired, `deny`
arrives as a `permissionDecision`, and `advise` arrives as `additionalContext`,
which puts the explanation in front of the model rather than the person. The
MCP-call wiring is transport-complete but rule-empty today: the wiring passes
every call because nothing yet judges an MCP tool name, not because the
channel cannot carry a verdict.

**The guard rewrites no command.** Claude Code hands a shell command `/dev/null` for its stdin, even when its own stdin is an open pipe (re-probed 2026-10-13 on 2.1.295), so a stray reader (grep with no file operand, `read`, a prompt, ssh, a pager) reads end-of-file instead of waiting. A reply carries no `updatedInput`, and the call runs exactly as Claude Code sent it, under your own permission rules.

This harness wires six events: `PreToolUse`, `PostToolUse` (a finished spawn's child
id), `SessionStart`, `Stop` (the checkpoint), `SubagentStop` (a
subagent's context size) and `UserPromptSubmit`. Every entry takes only `--agent-name`;
the spell's `mgs_getLimits` carries Claude Code's Bash timeout limits
(`BASH_DEFAULT_TIMEOUT_MS` and `BASH_MAX_TIMEOUT_MS`), which magus applies to a call's
`timeout` and `run_in_background`, and
`resumesOnBackgroundFrom = 2.1.299`, the first Claude Code release that resumes a
stopped subagent when a background task it left running completes. From that version a
subagent's stop with a task still running stands, and the subagent keeps its job. Below
it, or when the transcript records no version, the stop is refused.

That is not the same as using everything this host offers. `PostToolUseFailure`,
`PermissionRequest` and `PreCompact` are available and unused, on the test every
wiring here has to pass: a hook must change a verdict or restore state the model
cannot otherwise get. An advisory that fires every turn to restate guidance the
skills already carry fails it.

`UserPromptSubmit` passes that test without saying anything. The guard glue hands
the message the person typed to `magus shell --message`, which records a topic marker when
the message asks a structure question (architecture, boundaries, layering, imports,
a new package, where something belongs, blast radius) and prints nothing, so the
model's context gets no advisory and the message is never blocked. The marker
changes a later verdict: `architecture-before-skill` holds the agent's next call
until it loads `magus-architecture-review`. The message text is not kept.

`SubagentStart` is the one worth naming, because it looks like it should replace
the [lease wiring](#lease-capture) above and does not. It fires when a subagent
is spawned and matches on agent type, but its documented input does not carry the
prompt the orchestrator handed over, and the prompt is the whole record: it is
what magus stores as the spawn's payload, and it is where a `lease:` marker rides.
So the `PreToolUse` matcher `Task` wiring stays, on the tool call that does carry
`tool_input.prompt`.

## Verify

```sh
magus doctor
```

`doctor`'s **guard binary** check names the binary a hook would run and
fails when it is older than your working tree; **guard wiring** probes it with a
known-denied command and then looks for a host config that invokes a current
template; **agent skills** grades the installed copies against the running binary
and `--fix` reinstalls whatever it reports stale.

Commit `.claude/settings.json` once you are happy with it. Until a checkout has
that file, its guard rules are correct and entirely unenforced, with nothing in
the session saying so, which is the gap the **guard wiring** check exists to
report.
