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

The spell installs entries for commands, file edits, Magus MCP tool calls, reads
(recorded and judged), and subagent spawns. Each is one `magus buzz` command that
runs a shipped script, which talks to `magus shell`: file edits run
`magus-path.buzz`, reads also run `magus-observe.buzz`, and every other entry runs
`magus-command.buzz`. The Bash entry, as `magus describe harness claude-code` prints it, in the place it lands in
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
          "command": "magus buzz -C \"$CLAUDE_PROJECT_DIR\" -s docs/guides/integrations/agents/magus-command.buzz -- --agent-name claude-code"
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
So the string is the same on every machine, never an absolute path. `-C` makes the
root the working directory before anything else, so the root-relative script path
resolves wherever the session is; the glue reads the session's own directory off
the event. The glue is Buzz and needs no `jq`, and no entry carries any shell of
its own.

Flags an entry declares ride after `--`, which `magus buzz` forwards to the script,
never in a leading `VAR=value`: the glue reads whether to forward the whole event
off the event itself.

### Which magus runs

Three things in a session resolve the word `magus`, and each has its own owner:

| Who runs `magus`                                        | Resolved by                                                               | Owner                                                                                                                                              |
| ------------------------------------------------------- | ------------------------------------------------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------- |
| Hook commands (the guard's interpreter)                 | PATH, then the `./magus` of the tree `-C` names when that tree builds one | every hook entry, and magus's own re-exec                                                                                                          |
| The binary that judges a call                           | `./magus` beside the nearest `magus.yaml` above the call's cwd, else PATH | `magus-command.buzz` and the other glue, from the event's `cwd`                                                                                    |
| The agent's own Bash tool commands                      | PATH, with the session root put first                                     | the `SessionStart` entry (matcher `startup\|resume\|clear`), whose `magus-session.buzz` appends `export PATH="<root>:$PATH"` to `$CLAUDE_ENV_FILE` |
| Commands magus itself starts (targets, spells, scripts) | PATH, with the running binary's directory put first                       | magus, on every child it spawns                                                                                                                    |

A hook's `magus` is whatever the PATH Claude Code was started with finds. Before it
does anything else, that magus walks up from the `-C` directory to the workspace root
and re-execs into the `./magus` there when one exists, so a workspace that builds its
own binary is judged by it and one that builds none uses the installed one. The
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
`magus\guard.builtins`) or the checkout holds no `./magus`, the guard denies every write:
file edits, spawns, pushes and every shell command that changes state, and the magus MCP
tools that write: `client`, which runs a script, and `diff` with any `op` other than
`state`. The tools that only read (`status`, `config`, `console`, `buzz`, `diff` with
`op=state`) still answer. Reads, `git status` and the fix still run, and the denial names the one command to run. The workspace
needs only to show a guard rule in its magusfile for this, so a fresh worktree is covered
before any policy has loaded in its cache.

The fix depends on who is asking. The orchestrator or a person rebuilds with `./magus
run go-build .`, or bootstraps with the command the denial prints where the checkout has
no binary. A worker holding a lease never builds one: there is one binary per base, the
orchestrator builds it in the root, and `hack/dev/bootstrap-worktree.buzz` places a copy
in the worker's checkout, so the denial tells the worker to ask for that. A worktree
session with no `./magus` is an error, where it used to run open on whatever PATH held;
`SessionStart` says so once per session, naming the one fix, whenever the binary that
would judge the session cannot load the tree.

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
so `magus describe harness claude-code` prints this entry alongside the shell-command
and file-write entries:

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
denying or advising on one, which is the honest state to ship rather than
silence. The next rule the guard grows reaches the model the moment it
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

When Claude Code hands work to a subagent it does so through a tool call, and
that call fires `PreToolUse` like any other, carrying the whole prompt the
orchestrator is handing over in `tool_input.prompt`, the callee's declared
`subagent_type`, and, when the caller named one, `tool_input.model`. The shipped
descriptor includes a matcher for it, so `magus describe harness claude-code`
prints this entry alongside the entries above:

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

Same script as the MCP-call entry and for the same reason: a spawn's payload is a
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
declared model stored as a payload blob you fetch by ref: `magus session show
<id>` renders each spawn's model claim, wording an absent one as "none
declared" rather than leaving it blank.

The matcher covers both names Claude Code has used for the spawn tool across
releases (`Task` historically, `Agent` currently); a release that emits neither
records nothing here; nothing else in the contract depends on the spelling.

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
workspace-root walk as the lease hook above, for the same reason.
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
somewhere else. Run `magus session --brief` yourself to see what a session
is handed.

## The magus mod

The repository is a Claude Code plugin marketplace with one mod in it, `magus`,
whose source is `claude-code-mod/` beside this page. Install it once per person:

```text
/plugin install magus --marketplace egladman/magus
```

It adds three things to a session:

- A status line entry and a band above the prompt that name a magus problem with the
  command that fixes it: no binary, a borrowed or stale `./magus`, a stopped daemon,
  a daemon running another version, or an MCP endpoint that is not serving.
- A pane, opened with `/magus`, of the job tree, recent runs, and the session's
  subagents. It reads the daemon's Connect API over its unix socket, the same
  services the console uses, so it needs `magus server start` and holds no token.
- Prompt-cache warnings: a countdown before the cache expires, and a warning or a
  held prompt when you submit after it has, priced from a rates table you can
  replace through the mod's `ratesFile` setting.

Every threshold is a setting in `/config`. The mod uses the early-access Claude
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
reads the marketplace only once the person trusts the folder, and each person still
runs the install line above once.

## Coverage and limits

No transport gap in the guard contract: all three kinds of input are wired, `deny`
arrives as a `permissionDecision`, and `advise` arrives as `additionalContext`,
which puts the explanation in front of the model rather than the person. The
MCP-call wiring is transport-complete but rule-empty today: the wiring passes
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
