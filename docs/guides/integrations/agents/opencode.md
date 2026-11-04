---
title: OpenCode
description: Wiring magus into OpenCode (skills in .opencode/skills, the Go/Buzz WebAssembly plugin that carries the shell-command, file-write, MCP and subagent rules, the idle checkpoint, and the opt-in reply check).
tags: [agents, opencode, skills, guard, plugin]
---

# OpenCode

OpenCode discovers Agent Skills and intercepts tool calls through a plugin
rather than a hook config. Throwing from `tool.execute.before` blocks the call, and
the thrown message reaches the model as the tool error; an advise is appended to the
tool's own result by `tool.execute.after`, which is the same call and the same context
window. One file carries all of it. Everything below was checked against OpenCode
1.18.30.

| what            | where                                                                                          |
| --------------- | ---------------------------------------------------------------------------------------------- |
| skills          | `.opencode/skills/` (it also reads `.claude/skills/`)                                          |
| guard wiring    | `.opencode/plugins/`, per project; never `~/.config/opencode/plugins/`                         |
| shell commands  | `bash`: deny and advise both reach the model                                                   |
| file writes     | `edit`, `write` and `apply_patch`: deny and advise both reach the model                        |
| MCP calls       | the magus server's tools, matched by the `<server>_<tool>` id: deny and advise reach the model |
| subagent spawns | the `task` tool: judged before, the child's id recorded after; a child's calls name their root |
| typed messages  | the `chat.message` hook records them with `magus shell --message`                              |
| checkpoint      | the `session.idle` bus event, root sessions only                                               |
| reply check     | opt-in, on the same idle event: findings go back as a follow-up prompt (see below)             |
| scout agent     | `.opencode/agents/magus-scout.md`, `permission: edit: deny`                                    |
| rehydration     | not wired: the plugin does not use `experimental.session.compacting` (see below)               |
| MCP config      | [MCP](../mcp.md)                                                                               |
| quiet output    | the plugin's `shell.env` hook sets `MAGUS_LOG_SILENT=true` unless you already did              |

## Skills

```sh
magus agent install .opencode/skills
```

Prefer wiring the OpenCode harness from the root magusfile so skills install
and multi-host apply see it beside the other hosts:

```buzz
import "magus";
import "ghcr.io/egladman/magus/spells/harness/opencode";
magus\harness.provider(opencode);
```

Declare the spell in `magus.yaml` with the tag cd's `spell-publish` step pushed, and
run your lock target with `:update` to pin its digest in `magus.lock`, so the harness
versions apart from your magus binary; see
[Remote spells](../../../reference/remote-spells.md).

`magus agent harness install --id opencode` installs the complete plugin runtime,
its helper scripts, tool metadata, skills, and declared permissions. The plugin
requires a small JavaScript entry, a Go WebAssembly module containing the Buzz
program, and the matching Go runtime support file. These assets ship with Magus;
no separate plugin download or TypeScript build is required.

Run `magus agent harness verify --id opencode` after installation, then restart
OpenCode to load changes. Verification distinguishes missing runtime files from
skills-only installations and reports synthetic activation separately from a real
OpenCode session.

## MCP

Configure MCP for OpenCode yourself. `magus describe harness opencode`
prints a short secret-ref hint and a docs pointer; Magus does not write OpenCode
MCP config. See [MCP](../mcp.md). An agent uses the CLI fallback when MCP is
unavailable.

The plugin judges calls to magus's own MCP server the way it judges the CLI. OpenCode
names an MCP tool `<server>_<tool>`, with every character outside `a-zA-Z0-9_-` in
either part replaced by `_`, so a server configured under `mcp.magus` exposes
`magus_client`. The plugin finds your magus server in the `mcp` object of the config
OpenCode loads: the entry whose local command names `magus` and `mcp`, or a key called
`magus` that is not a local command. Under any other setup its calls are not
recognized as magus's: they go to magus whole under their own name, like every other
tool the plugin does not judge by name, and the unjudged-tool rule names them (see
[Every tool reaches the guard](guard.md#every-tool-reaches-the-guard)).

## Quiet output

`MAGUS_LOG_SILENT=true` is `-s` on every magus command: verdicts, refs and failures,
without each reason or a wait note under a minute. `opencode.json` documents no key for the
environment of the commands the agent runs, so the harness writes none and
`magus describe harness opencode` says so. The plugin below sets it instead: its
`shell.env` hook exports `MAGUS_LOG_SILENT=true` into every command the agent runs,
unless the variable is already set in the environment you started OpenCode from.
To choose another value, set it there:

```sh
MAGUS_LOG_SILENT=false opencode
```

## Guard hook

Install the harness with `magus agent harness install --id opencode`, then confirm OpenCode loaded it:

```sh
opencode debug config
```

It encodes no magus rule. Every decision comes from `magus shell`, which keeps it
host-only glue rather than a second rule set that drifts out of step with the
other templates.

The plugin implementation lives in Go and Buzz. The embedded JavaScript entry only loads WebAssembly and returns its hooks.

The thing being judged goes in on stdin, never in argv. `magus shell` takes no
positional arguments, and a plugin that passes the command as one does not get a
wrong verdict; it gets no verdict, which fails open on every call.

If magus lives in a prefix the OpenCode process does not have on PATH (mise,
brew, asdf, `~/.local/bin`), set `__MAGUS_BIN` to an absolute path.

A push at a commit no passing gate covers needs your approval, and a plugin cannot
ask: `tool.execute.before` can only throw. `magus describe harness opencode`
prints an `"ask"` entry under `permission.bash` in `opencode.json`, and the command
that merges it, for each backend's
push, bare and with arguments: `git push`, `hg push`, `sl push` and `jj git push`, each
also as `<verb> *`. OpenCode then prompts before every push, and the plugin's
`permission.ask` hook answers that prompt from the verdict: allow for a push a gate
covers, ask for an ungated one, deny for a session bound to a job lease, because
workers do not publish. Without the permission for that backend, or for a push written
as anything but a plain `git push`, `hg push`, `sl push` or `jj git push` command, the plugin refuses the push and names your own
terminal. `magus agent harness verify --id opencode` fails while the permission is
missing. A decision the plugin does not know is refused, never allowed.

A target the workspace declares `human_only` is asked about the same way. The
harness prints `magus run <target>`, `./magus run <target>`, `magus affected <target>`
and `./magus affected <target>` for each one, bare and as `<prefix> *`, and the plugin
answers OpenCode's prompt from the verdict: ask for a session no job lease binds, deny
for a leased worker. A run spelled any other way, such as with a flag before the
target, is refused, naming your own terminal.

## Notifications

Invoke `magus session notify` from the plugin with the same envelope every other host
uses; see [Attention hooks](notifications.md).

## Handing a compacted session its state back

`experimental.session.compacting` runs while OpenCode is building the summary
that will replace a session's history, and takes `context: string[]` straight
into the compaction prompt. **The plugin deliberately does not use it**, and a
compacted OpenCode session is therefore handed nothing.

That hook feeds the SUMMARIZER, not the model that comes after it. The plugin
used to push `magus session --brief` through it, which read as rehydration and
was not: on Claude Code and Codex the brief lands verbatim after the summary,
while here it went in as summarizer input and survived only as much of it as the
summarizer kept. The brief is state read off the disk, and this was the one place
it was retold.

The second reason is parity. Of the four hosts, only OpenCode can shape a summary
at all: Claude Code exposes no `PreCompact` output arm, Codex's
`pre-compact.command.output` schema is closed over four fields with no context
channel, and Cursor's `preCompact` is observational. A behavior available on one
host of four is a difference nobody can reason about, and deciding what a model
remembers is not magus's call to make.

OpenCode does announce a finished compaction: the `session.compacted` event is part
of its SDK's event union. It arrives on the read-only bus, so the plugin could observe
it and could not put text in front of the model the way a hook's output does; the
plugin does not subscribe to it. Run `magus session --brief` yourself after a
compaction: it prints the same state on demand, which every host shares.

## Recording where the work stands

OpenCode has no session-end event. `session.idle` on the read-only bus is the
proxy its own docs name, and the plugin subscribes to it and runs
`magus session checkpoint --agent-name opencode`: the revision, branch and
dirtiness of the tree, which `magus session` then lists.

Idle fires when the agent stops working rather than when the session is closed,
so a long session records several checkpoints. That is the same shape a `Stop`
hook produces on the hosts that have one, and the listing is ordered.

A subagent's session goes idle too, once per spawn. The plugin tells a subagent's
session from a root one by the session's `parentID`, which the `session.created`
event carries (and the SDK's `session.get` answers when the plugin did not see the
event), and skips the subagents: they record no checkpoint and get no reply check.

If you would rather not have the plugin do it, running
[`magus-checkpoint.buzz`](guard-templates.md#magus-checkpointbuzz) with
`--agent-name opencode` records the identical row.
`magus session checkpoint --note "..."` writes it by hand.

## The reply check and the scout

The reply check judges your agent's replies with the `proofread` binary, which is a
choice about how your agent writes and not part of the guard, so the plugin does
nothing about replies until you turn it on. It has no stop hook to ride on here, and
`session.idle` is on a read-only bus: a subscriber cannot return text from it. The
plugin reaches the agent another way. With the check on, on a root session's
idle event it reads the last assistant reply with the SDK's `session.messages`, runs
[`proofread-reply.buzz`](guard-templates.md#proofread-replybuzz) with
`--agent-name opencode` over it, and when proofread has findings sends them back with
`session.promptAsync`, a new user message in the same session. The script's
`opencode` reply shape is `{"prompt": ...}`.

- One follow-up per turn. The idle after the follow-up is the follow-up's own reply
  and is not judged; a message you type clears that, so your next turn is checked
  again.
- To turn it on, download `proofread-reply.buzz` and set `__PROOFREAD_REPLY_BUZZ` to
  its path in the environment that starts OpenCode. The plugin looks nowhere else:
  with the variable unset or naming a file that does not exist, or without the
  `proofread` binary, the check is silent.
- A headless `opencode run` exits at the first idle, before the follow-up is
  answered: the follow-up is accepted and never runs. The check does work when the
  session lives in a server: start `opencode serve` and drive it with
  `opencode run --attach <url>`. Both were run against 1.18.30: the attached
  session went idle a second time after the follow-up, the headless one did not.

The scout agent's file sets `permission: edit: deny`, which OpenCode applies to
every file-changing tool, so the read-only promise in its instructions is a
setting as well. `bash` stays at the session's own setting, because the scout's job
is running read-only magus commands.

## Coverage and limits

- **An ask is OpenCode's own prompt, where one can happen.** The prompt comes from the
  `permission.bash` entries the harness writes; a plugin can only throw. A call those
  entries do not reach is refused, and the only channel for that refusal is the tool error,
  which the model reads. The plugin does not show the person a separate notice, and the
  refusal asks the agent to tell them. Not verified against a client: that the plugin's
  `permission.ask` hook, which OpenCode's plugin page does not list, keeps working past
  1.18.30, and that a remote client answering through
  `POST /session/:id/permissions/:permissionID` is served a prompt this hook raised.

- No `mgs_listEntries` is merged: OpenCode has no hook config, so the spell
  writes the agent files and the push permission, and the plugin is the only hook.
  The plugin wires `tool.execute.before` and `.after`, `permission.ask`,
  `chat.message`, `shell.env`, `config` (to learn the magus MCP server's key) and
  the `event` hook (`session.created`, `session.updated`, `session.idle`). It does
  not use `experimental.session.compacting`.
- An `advise` verdict is appended to the tool result that call produced, joined
  to it by `callID`. It is judged once, before the call, and delivered after it:
  judging again in `tool.execute.after` would record two verdicts for one call.
  An advised call whose result never arrives strands one string for the life of
  the session, which is the cheaper of the two leaks. An MCP tool's result has a
  `content` array and no `output` string, and a string set on `output` is dropped,
  so there the advisory is pushed as a text part of `content`; that is what reaches
  the model.
- A thrown deny reaches the model. A `bash` call whose hook threw came back to the
  model as the tool error with the thrown message, and the model repeated its text
  (OpenCode 1.18.30).
- The plugin finds the workspace's `./magus` from the nearest `magus.yaml`, so a
  session opened in `console/` or any other project directory still uses the
  root's build.
- A magus that runs but cannot load the workspace does not fail open: it answers
  `magus shell` with a deny for every call that changes state, and the plugin
  throws that reason as the tool error. Reads and the rebuild still pass. The
  orchestrator or the person rebuilds `./magus` (`./magus run go-build .`, or the
  bootstrap the deny names); a leased worker asks the orchestrator to place one.
- The plugin fails open when magus cannot be run at all, and says so in the log.
  Throwing is OpenCode's only way to stop a call, so a guard that threw on a
  missing binary would block every tool call and make the session unusable.
- Tool identifiers were read from live calls on OpenCode 1.18.30: `bash` carries
  `command`; `edit` carries `filePath`, `oldString` and `newString`; `task` carries
  `subagent_type`, `prompt` and `description`. An MCP tool arrives as
  `<server>_<tool>` with both halves sanitized (OpenCode's `McpTool.name`), for
  example `echo_say_hi` from a server `echo`. `apply_patch`, the file-changing tool
  a gpt-* model gets in place of `edit` and `write`, carries `patchText` (OpenCode's
  `apply_patch` tool source); the plugin forwards it whole as an
  `apply_patch` envelope and magus reads the files it names.
- A spawn is judged by the `task` call's prompt before it runs. When it finishes,
  its metadata names the child session (`metadata.sessionId`) and the plugin
  reports that id to magus. A child session's `session.created` event carries
  `parentID`, and every call a subagent makes goes to magus as the root session
  with the child as the agent, which is how a rule tells a worker's call from the
  main conversation's. A subagent's first message is the spawn prompt, so
  `chat.message` skips child sessions.
- A message you type goes to `magus shell --message`, which records the topics it
  raises and judges nothing. Text pushed into a `chat.message` part reaches the model
  on 1.18.30, but this plugin pushes none: that hook only records.
- `shell.env` does not export `BAGGAGE=magus.lease=<id>`: the only place the plugin could read that id is the
  marker `magus job exec` writes into the checkout, and the guard and the sandbox
  already read that marker directly. A copy would be a second source of truth that
  can go stale.

## Verify

```sh
opencode debug config
magus doctor
```
