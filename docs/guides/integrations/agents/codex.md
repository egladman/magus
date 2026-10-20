---
title: Codex
description: Wiring magus into Codex (skills in .agents/skills, the AGENTS.md block you paste, MCP in the user-level config, and the hooks that carry the guard, the spawn check, the message record, the checkpoint, the opt-in reply check and the post-compaction brief).
tags: [agents, codex, skills, AGENTS.md, guard, hooks, MCP]
---

# Codex

Codex reads two things, and it wants both: `.agents/skills/` for focused
workflows it loads on demand, and `AGENTS.md` for guidance that is always on.
Its hook payloads differ from Claude Code's, but both hosts accept the same
verdict shape. Current Magus binaries therefore receive those events directly;
the shared templates remain the portable fallback for hosts without that host
contract.

| what            | where                                                           |
| --------------- | --------------------------------------------------------------- |
| skills          | `.agents/skills/`                                               |
| always-on rules | `AGENTS.md` (you paste the block; magus never writes it)        |
| guard wiring    | `.codex/hooks.json`, `PreToolUse`                               |
| shell commands  | deny and advise both reach the model                            |
| file writes     | deny and advise both reach the model                            |
| MCP calls       | `PreToolUse` (`mcp__.*`), deny and advise both reach the model  |
| push approval   | `.codex/rules/magus.rules` prompts, `PermissionRequest` answers |
| subagent spawns | `PreToolUse` on the spawn tool, judged as a spawn               |
| subagent brief  | `SubagentStart`, the checkout's state as the child's context    |
| subagent record | `PostToolUse` names the child, `SubagentStop` sizes it          |
| your messages   | `UserPromptSubmit`, recorded and never answered                 |
| checkpoint      | `Stop`                                                          |
| reply check     | opt-in: `Stop`, a `block` that continues the turn once          |
| scout agent     | `.codex/agents/magus-scout.toml`, `sandbox_mode = "read-only"`  |
| rehydration     | `SessionStart` (`compact`)                                      |
| MCP             | `~/.codex/config.toml`, see [MCP](../mcp.md)                    |
| quiet output    | `[shell_environment_policy.set]` in `~/.codex/config.toml`      |

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

Register the server in your user-level `~/.codex/config.toml`, never in the
repository. `magus describe harness codex` prints the fragment to paste
(secret ref `MAGUS_MCP_TOKEN`); Magus does not write that file:

```sh
magus describe harness codex
```

```toml
[mcp_servers.magus]
url = "http://127.0.0.1:7391/mcp"
bearer_token_env_var = "MAGUS_MCP_TOKEN"
enabled = true
```

```sh
magus config mcp connector create --name codex --expires 366d   # shown once: store it as MAGUS_MCP_TOKEN
codex mcp list
magus status --probe=liveness,mcp
```

`codex mcp list` confirms configuration; the probe confirms the endpoint is
serving. [MCP](../mcp.md) covers dedicated connector tokens, the ChatGPT desktop
app, and what to do when `mcp.address` changes. This is user-owned host setup;
an agent that cannot reach MCP uses the CLI fallback and does not manually start
Magus solely to obtain tools.

## Quiet output

`MAGUS_LOG_SILENT=true` is `-s` on every magus command: verdicts, refs and failures,
without each reason or a wait note under a minute. Codex sets the values in
`shell_environment_policy.set` on every subprocess it spawns, so the same paste
carries a second table:

```toml
[shell_environment_policy.set]
MAGUS_LOG_SILENT = "true"
```

The harness merges only `.codex/hooks.json`, which has no place for an
environment variable, and never writes `config.toml`, so this stays a paste. It
is a table header rather than a dotted key because a dotted key pasted after
`[mcp_servers.magus]` would land inside that table. If your config already
defines `shell_environment_policy.set`, add the variable there instead.

## Guard hook

Check that hooks are on before wiring anything:

```sh
codex features list
```

The `hooks` row reports the stage and whether it is enabled. Hooks are stable and
enabled by default; read the row when working with an older local build.

Prefer wiring the Codex harness from the root magusfile when you bounce between
hosts; `magus describe harness` then covers every wired provider:

```buzz
import "magus";
import "ghcr.io/egladman/magus/spells/harness/codex";
magus\harness.provider(codex);
```

Declare the spell in `magus.yaml` with the tag cd's `spell-publish` step pushed, and
run your lock target with `:update` to pin its digest in `magus.lock`, so the harness
versions apart from your magus binary; see
[Remote spells](../../../reference/remote-spells.md).

```sh
magus describe harness
magus agent harness verify
```

`magus describe harness` prints each entry the host files lack and the one command
that merges them. magus never writes them: read the command, run it yourself, then
verify.

To adapt that Buzz harness without modifying Magus source: copy the spell into
the workspace, change only the import path (for example
`import "harness/codex" as codex`), edit the workspace Buzz, then describe, merge,
and verify again. Details:
[Adapting a Buzz harness](../../../reference/skills/magus-workspace-rules.md) and
[Recurring guard friction](guard.md#recurring-guard-friction).

Or target Codex alone:

```sh
magus describe harness codex
magus agent harness verify --id codex
```

The spell describes `.codex/hooks.json` with the guard entries
shown here:

```json
{
  "hooks": {
    "PreToolUse": [
      {
        "matcher": "*",
        "hooks": [
          {
            "type": "command",
            "command": "magus buzz -C . -s docs/guides/integrations/agents/magus-command.buzz -- --agent-name codex --tool-rows .codex/magus/tools",
            "statusMessage": "magus guard: checking the call"
          }
        ]
      }
    ],
    "PostToolUse": [
      {
        "matcher": "spawn_agent|collaborationspawn_agent|Agent",
        "hooks": [
          {
            "type": "command",
            "command": "magus buzz -C . -s docs/guides/integrations/agents/magus-command.buzz -- --agent-name codex --tool-rows .codex/magus/tools",
            "statusMessage": "magus: recording the subagent"
          }
        ]
      }
    ],
    "SubagentStart": [
      {
        "hooks": [
          {
            "type": "command",
            "command": "magus buzz -C . -s docs/guides/integrations/agents/magus-rehydrate.buzz -- --format json --rules AGENTS.md",
            "statusMessage": "magus: briefing the subagent"
          }
        ]
      },
      {
        "hooks": [
          {
            "type": "command",
            "command": "magus buzz -C . -s docs/guides/integrations/agents/magus-command.buzz -- --agent-name codex --tool-rows .codex/magus/tools",
            "statusMessage": "magus: joining the subagent to its spawn"
          }
        ]
      }
    ],
    "SubagentStop": [
      {
        "hooks": [
          {
            "type": "command",
            "command": "magus buzz -C . -s docs/guides/integrations/agents/magus-command.buzz -- --agent-name codex --tool-rows .codex/magus/tools",
            "statusMessage": "magus: recording the subagent's context"
          }
        ]
      }
    ],
    "PermissionRequest": [
      {
        "matcher": "Bash",
        "hooks": [
          {
            "type": "command",
            "command": "magus buzz -C . -s docs/guides/integrations/agents/magus-command.buzz -- --agent-name codex --tool-rows .codex/magus/tools",
            "statusMessage": "magus guard: checking approval"
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
            "command": "magus buzz -C . -s docs/guides/integrations/agents/magus-rehydrate.buzz -- --format json --rules AGENTS.md",
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
            "command": "magus buzz -C . -s docs/guides/integrations/agents/magus-checkpoint.buzz -- --agent-name codex",
            "statusMessage": "magus: recording where the work stands"
          }
        ]
      }
    ],
    "UserPromptSubmit": [
      {
        "hooks": [
          {
            "type": "command",
            "command": "magus buzz -C . -s docs/guides/integrations/agents/magus-command.buzz -- --agent-name codex --tool-rows .codex/magus/tools",
            "statusMessage": "magus: noting the request"
          }
        ]
      }
    ]
  }
}
```

Every entry runs the Buzz glue as a plain argv: `magus buzz -C . -s <file>`, with the
host and any flags after `--`. It needs neither jq nor a POSIX shell. The harness
descriptor only merges those opaque fragments into `.codex/hooks.json`; Magus does
not inject a codec. Copy `docs/guides/integrations/agents/codex-hooks.json` from the
repository, or merge what `magus describe harness codex` prints.

Each entry names `.codex/magus/tools` with `--tool-rows`. `magus agent harness install`
writes the spell's tool rows there, and a tool the rows list as touching nothing passes
before any rule loads. [Every tool reaches the guard](guard.md#every-tool-reaches-the-guard)
has the rest.

The two `PreToolUse` entries used to carry `__MAGUS_NO_ADVISE=1`, which rendered
every advisory as nothing. That rested on a claim OpenAI's current hooks
reference contradicts: `additionalContext` is a supported `PreToolUse` field, and
the response keys Codex rejects, the ones that make it mark a hook run failed and
continue the call, are `continue`, `stopReason` and `suppressOutput`. Suppression
cost this host every explanation the guard had to give while enforcing every deny,
which is the half of the contract nothing in a session reports missing. If your own
build behaves otherwise, `__MAGUS_NO_ADVISE=1` still suppresses the arm.

### Push approval

A push no passing gate covers needs the person's approval. Codex hooks cannot ask:
Codex parses `permissionDecision: "ask"`, marks the hook run failed, and runs the
call anyway, so the template never sends it. The prompt comes from
`.codex/rules/magus.rules`, which `magus describe harness codex` prints: a `prefix_rule` with
`decision = "prompt"` for each backend's push, `git push`, `hg push`, `sl push` and
`jj git push`. Before that prompt Codex raises `PermissionRequest`, and the
same command template answers it: allow for a push a gate covers, so nobody is
asked, no decision for an ungated push, so the person decides, and deny for a
session bound to a job lease, because workers do not publish. Project rules load
only when the project's `.codex` layer is trusted, the same trust that loads its
hooks. Where no prompt can happen (no rules file, `permission_mode` of
`bypassPermissions` or `dontAsk`, or a push written as anything but a plain
`git push`, `hg push`, `sl push` or `jj git push` command), the guard refuses the push and names the person's own
terminal. One open Codex bug ignores a prompt rule under `danger-full-access` with
granular rules, [openai/codex#25312](https://github.com/openai/codex/issues/25312).
The template refuses a session whose hook input reports `bypassPermissions`, but
nothing here has confirmed that full access reports that mode, so do not run pushes
in full access until the bug closes. `magus agent harness
verify --id codex` fails while the rules file or the `PermissionRequest` entry is
missing.

### Maintaining the workspace harness

This host is a Buzz harness spell. Adapt without Magus source edits by forking
the spell and changing only the import path; then merge what `magus describe
harness` prints and run `magus agent harness verify`. See
[Adapting a Buzz harness](../../../reference/skills/magus-workspace-rules.md) and
[Recurring guard friction](guard.md#recurring-guard-friction).

The `Stop` entry is not a guard. It records where the work stands each time a
turn ends, which is worth having here in particular: a Codex session that runs
out of usage stops mid-task, and the transcript it leaves behind is addressed by
a session id nobody wrote down. `magus session` lists what it recorded, and
`magus session checkpoint --note "..."` is the same record made by hand.

### Judging the reply a turn ends with

Nothing here is wired by default: the reply check judges your agent's replies with
the `proofread` binary, which is a choice, not part of the guard. To turn it on, download
[`proofread-reply.buzz`](guard-templates.md#proofread-replybuzz) and add a second `Stop`
entry to `.codex/hooks.json`:

```json
{
  "hooks": {
    "Stop": [
      {
        "hooks": [
          {
            "type": "command",
            "command": "magus buzz -C . -s docs/guides/integrations/agents/proofread-reply.buzz -- --agent-name codex",
            "statusMessage": "magus: proofreading the reply"
          }
        ]
      }
    ]
  }
}
```

The script runs with `--agent-name codex`. It reads `last_assistant_message`, runs the `proofread`
binary over it, and, when there are findings, prints
`{"decision": "block", "reason": "..."}`. Codex continues the turn with the reason
as a new prompt, so the findings reach the agent as a request to send the reply
again, fixed. Codex's `Stop` output schema takes no `hookSpecificOutput`, which
is why this host cannot receive the advice-only shape Claude Code does. The
continuation's own `Stop` carries `stop_hook_active`, and the hook stays silent
on it, so a reply is judged once. It needs `proofread` on `PATH` or in
`__PROOFREAD_BIN`, prints nothing without it, and `__PROOFREAD_DECISIONS` names a
decisions file. Codex shows the entry's `statusMessage` while it runs.

### Messages, spawns and the scout

`UserPromptSubmit` hands the message you typed to `magus shell --message`, which
records the topics it raises and judges nothing. The catch-all `PreToolUse` entry runs
the command glue on a spawn too, which magus judges the way it judges Claude Code's
`Agent` call. The finished spawn's `PostToolUse` matcher is
`spawn_agent|collaborationspawn_agent|Agent`: a matcher made only of letters, digits
and underscores is an exact list of tool names, and v2 models call the tool
`collaborationspawn_agent`, which a bare `spawn_agent` entry never fires on. `Agent` is
the alias Codex's docs name. No entry declares `--observes-skill-loads`: Codex has
no hook on a skill load, so a rule that requires one has nothing to read and stands
down instead of denying forever.

The spawn's input differs by model. A v2 spawn carries `task_name`, `fork_turns` and a
`message` that is encrypted (`gAAAA...`), so the brief the parent wrote cannot be read,
and no `lease:` marker can ride in it. A v1 spawn carries a plain `message` or `items`,
an `agent_type`, and optionally a `model` and `reasoning_effort`. Either way there is no
`prompt` field; magus reads `message`, `items` and `task_name` by shape.

Three more hooks complete the picture of a subagent:

- `PostToolUse` on the same matcher receives the finished spawn. Its `tool_response` is
  a JSON string: `{"task_name":"/root/x"}` for v2, `{"agent_id":...,"nickname":...}` for
  v1. It judges nothing; magus files the name or id against the spawn.
- `SubagentStart` hands the child its brief. The command is
  [`magus-rehydrate.buzz`](guard-templates.md#magus-rehydratebuzz) with
  `--format json`, answering as `SubagentStart`, so the checkout's state and live leases
  arrive as the child's `hookSpecificOutput.additionalContext`. This is the only channel
  that reaches a v2 child, and a live probe showed the child obeying it. The payload
  names the child (`agent_id`, `agent_type`) and nothing about the spawn that made it,
  so a second entry hands the same event to the command glue, which judges nothing and
  joins the child's id to the spawn just before it.
- `SubagentStop` carries `agent_id`, `agent_transcript_path` and
  `last_assistant_message`. magus reads the child's rollout for its last `token_count`
  record (`last_token_usage.input_tokens` against `model_context_window`) and files the
  context size a later continuation to that child reads. The child's own tool calls
  carry `agent_id` and `agent_type` too, so its commands are attributed to it.

The scout agent file sets `sandbox_mode = "read-only"`, so the sandbox enforces what
its instructions only ask for. Codex's read-only sandbox blocks writes, which may
include magus's own cache and activity records, so a query that needs one can fail
inside the scout. That has not been run live here.

## Handing a compacted session its state back

The `SessionStart` entry matches `compact`, the moment Codex replaces a long
session's history with a summary, and runs
[`magus-rehydrate.buzz`](guard-templates.md#magus-rehydratebuzz). What it prints is
this checkout: branch and revision, commits not yet on the base ref, the dirty
tree split into sources, generated outputs and unclaimed paths, the live leases,
the last recorded run's failures, and where the rules live.
When repeated guard feedback needs review, the same brief adds one bounded line
that directs the model to `magus doctor`; it does not edit memory or
instructions by itself.

Two flags shape it for this host. `--format json` is required, not cosmetic:
Codex reads a `SessionStart` hook's stdout as a JSON reply and adds
`hookSpecificOutput.additionalContext` to the developer context, so plain text
arrives nowhere. `--rules AGENTS.md` names the instruction file Codex
reads, since the template's default is another host's.

`compact` is the matcher wired here, matching what this repository dogfoods on
[Claude Code](claude-code.md#handing-a-compacted-session-its-state-back): it is
the moment state was LOST. Codex offers `startup` and `resume` as well; `resume`
answers the related question, since a session resumed after a break did not watch
the tree move while it was away.

Run `magus session --brief` yourself to read exactly what a compacted session
is handed.

## Notifications

Codex runs a program on its notify setting. Shape the event into the canonical
envelope and pipe it to `magus session notify`, exactly as the other hosts do; see
[Attention hooks](notifications.md) for the envelope and the vocabulary.

## Coverage and limits

- **Every tool reaches the guard.** One `PreToolUse` entry matches `*`, and the glue
  sends the whole event, so magus judges a command, a patch, an MCP call and a spawn by
  the shape of its input. A call to another MCP server's tool carries nothing magus can
  read, and the unjudged-tool rule names it (see
  [Every tool reaches the guard](guard.md#every-tool-reaches-the-guard)).
- Hooks are not available on Windows, and `[features] hooks = false` turns the
  every hook off. A repo-local `.codex/hooks.json` is also inert until you
  trust the project layer, and every non-managed hook wants a per-hash review
  through `/hooks`, so a config committed to a repository guards nobody who has
  not accepted it. That is the one way this host differs from the others in kind
  rather than degree.
- **The guard never rewrites a command.** Codex closes a command's stdin itself, and so do Claude Code and Cursor, so no host needs a `exec </dev/null;` prefix. A prefix would also move the command out of reach of your own `prefix_rule`: a probe ran `exec </dev/null; echo x` under a rule forbidding `echo x`, and it ran. Codex also applies `updatedInput` only beside `permissionDecision: "allow"` and marks the hook failed on any other shape, so a rewrite would turn every guard pass into an approval that skips Codex's own prompt. The glue never sends one.
- Codex has no `Read` tool; a file is read through a shell command, which the command
  rules already judge. No `Read` matcher is wired, and magus keeps no read record for
  this host. File writes are `apply_patch`, which the catch-all entry receives like any
  call. Its patch rides in `tool_input.command`, and magus reads the `*** Add File`,
  `Update File`, `Delete File` and `Move to` paths out of it as the files written.
- **MCP and `SessionStart(compact)` are verified against a live Codex 0.155.1.** An MCP
  call arrives as `PreToolUse` with `tool_name` `mcp__<server>__<tool>`, and the complete
  `tool_input` goes to the same glue as a shell command. A `compact` session start runs the rehydrate entry, and Codex reads its
  `hookSpecificOutput.additionalContext`.
- **A child is tied to the spawn just before it.** The v2 spawn's `message` is
  encrypted and its response names the child by `task_name`, while the child's own events
  carry an `agent_id` and `SubagentStart` names no task. So magus files each spawn that
  passes, and the next `SubagentStart` within two minutes joins its `agent_id` to the
  oldest spawn still waiting: the child is filed under that spawn's description, name and
  model, and a `<parent>/<role> <job>` description attributes it to the job, as a
  finished spawn does on Claude Code. Two spawns in flight at once are joined in the
  order they were made, which Codex does not promise to start them in. A session
  started under `BAGGAGE=magus.lease=<id>` is attributed to that lease by its
  environment, which a Codex child inherits.
- `ask` and the `Stop` schema limits hold. A hook `ask` is parsed, marked failed and the
  call continues, so the push prompt comes from the rules file. `Stop` output takes
  `decision: "block"` and its reason, no `hookSpecificOutput`, so advice from a reply
  check cannot ride a `Stop` reply; it reaches the model only as the prompt of the
  continuation turn.

### Events

Codex supports twelve hook events. This harness wires eight:

| event                       | wired | what runs                                                                                            |
| --------------------------- | ----- | ---------------------------------------------------------------------------------------------------- |
| `PreToolUse`                | yes   | every tool (`*`): commands, patches, MCP calls and spawns, judged by the shape of the whole event    |
| `PermissionRequest`         | yes   | answers the push approval                                                                            |
| `PostToolUse`               | yes   | the finished spawn, which names the child by `task_name` (v2) or `agent_id` (v1)                     |
| `SubagentStart`             | yes   | hands the child the checkout's brief as `additionalContext`, and joins its id to the spawn before it |
| `SubagentStop`              | yes   | files the child's context size from the last `token_count` record of its rollout                     |
| `SessionStart`              | yes   | `compact` only, the post-compaction brief                                                            |
| `Stop`                      | yes   | the checkpoint; the reply check is a second entry you add on purpose                                 |
| `UserPromptSubmit`          | yes   | records your message                                                                                 |
| `PreCompact`, `PostCompact` | no    | their output schema carries no `hookSpecificOutput`, so nothing can steer compaction                 |
| `Interrupt`                 | no    | nothing consumes it                                                                                  |
| `SessionEnd`                | no    | `Stop` already records the checkpoint each turn                                                      |

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
