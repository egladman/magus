---
title: Cursor
description: Wiring magus into Cursor - AGENTS.md for guidance, one self-contained hook script for all five of its events, and the one job Cursor's contract cannot express.
tags: [agents, cursor, AGENTS.md, guard, hooks]
---

# Cursor

Cursor does not read Agent Skills directories. It reads an `AGENTS.md` at the
repository root, and it runs hooks as programs with the event on stdin. One
self-contained script covers every event magus uses, so installing the whole
integration is a single download.

| what            | where                                                    |
| --------------- | -------------------------------------------------------- |
| always-on rules | `AGENTS.md` (you paste the block; magus never writes it) |
| guard wiring    | `.cursor/hooks.json`                                     |
| command surface | deny and advise both reach the model                     |
| file surface    | deny and advise both reach the model                     |
| checkpoint      | `sessionEnd`                                             |
| lease           | `subagentStart` (unverified live, see below)             |
| MCP             | [MCP](../mcp.md)                                         |

## Skills

There is no skills directory to install into. Run `magus agent install` anyway:
it prints the managed magus block when your `AGENTS.md` is missing it or
carrying a stale one, and you paste it in. [Skills](skills.md) covers the block,
its stamp, and the drift check that grades it.

Because Cursor has no Agent Skills surface, it cannot enforce a short-versus-full
skill-form choice. Keep that repository guidance explicit and user-owned in
`AGENTS.md`; do not claim a model or provider setting selects it automatically.

An `advise` verdict reaches the model here too, on `postToolUse` rather than at
the moment the call is gated; that guidance being in `AGENTS.md` as well is the
same belt-and-braces every host gets, not a substitute for it.

## MCP

Configure MCP for Cursor as a host-level integration; see [MCP](../mcp.md) for
the client configuration and token. An agent uses the CLI fallback when MCP is
unavailable; it does not manually start Magus solely to obtain tools.

## Guard hook

Save the script below as `.cursor/hooks/cursor-guard.sh`, make it executable,
and point every event at it:

```json
{
  "version": 1,
  "hooks": {
    "beforeShellExecution": [{ "command": "./.cursor/hooks/cursor-guard.sh" }],
    "preToolUse": [{ "matcher": "Write", "command": "./.cursor/hooks/cursor-guard.sh" }],
    "postToolUse": [
      { "matcher": "Shell", "command": "./.cursor/hooks/cursor-guard.sh" },
      { "matcher": "Write", "command": "./.cursor/hooks/cursor-guard.sh" }
    ],
    "subagentStart": [{ "command": "./.cursor/hooks/cursor-guard.sh" }],
    "sessionEnd": [{ "command": "./.cursor/hooks/cursor-guard.sh" }]
  }
}
```

It is deliberately self-contained rather than delegating to the shared
templates: needing five files to install a guard is how a guard ends up not
installed. The script branches on the `hook_event_name` every Cursor hook
carries, so one file serves all of them.

Cursor splits across two events what every other host delivers from one, and
that is the thing to read before changing this wiring. A gating event
(`beforeShellExecution`, `preToolUse`) carries a `deny`, because Cursor sends
`user_message` and `agent_message` only with a denial. An `advise` therefore has
no channel there and needs `postToolUse.additional_context`, which arrives after
the call. So a judged call runs magus twice on this host and leaves two rows in
the activity trail, where every other host leaves one.

The `matcher` values scope each entry to the tools that carry something to judge.
They are a bound on cost, not on correctness: the script answers on the SHAPE of
the payload, so a preToolUse event with no `tool_input.file_path` allows and a
postToolUse event with neither field returns `{}`.

```sh
#!/usr/bin/env sh
# magus guard for Cursor. ONE file, every event; download only this.
#
# Cursor runs a hook as a PROGRAM with the event as JSON on stdin. Its events carry
# different payloads, so this reads the event once and branches on the
# hook_event_name every one of them carries:
#
#   beforeShellExecution  {"command": "...", "cwd": "...", "sandbox": false}
#   preToolUse            {"tool_name": "...", "tool_input": {"file_path": "..."}}
#   postToolUse           the same, plus tool_output
#   subagentStart         {"subagent_type": "...", "task": "...", ...}
#   sessionEnd            {"session_id": "...", "reason": "..."}
#
# Save to .cursor/hooks/cursor-guard.sh, chmod +x, and point them at it:
#
#   {"version": 1, "hooks": {
#     "beforeShellExecution": [{"command": "./.cursor/hooks/cursor-guard.sh"}],
#     "preToolUse":   [{"matcher": "Write", "command": "./.cursor/hooks/cursor-guard.sh"}],
#     "postToolUse":  [{"matcher": "Shell", "command": "./.cursor/hooks/cursor-guard.sh"},
#                      {"matcher": "Write", "command": "./.cursor/hooks/cursor-guard.sh"}],
#     "subagentStart": [{"command": "./.cursor/hooks/cursor-guard.sh"}],
#     "sessionEnd":   [{"command": "./.cursor/hooks/cursor-guard.sh"}]}}
#
# Self-contained on purpose. The other hosts' templates delegate to
# magus-guard-command.sh, but Cursor would then need three files downloaded to
# work, and a guard nobody finishes installing guards nothing.
#
# WHICH EVENT CARRIES WHICH HALF of a verdict is the thing to read here, because
# Cursor splits across two events what every other host delivers from one:
#
#   - A DENY needs a gating event. beforeShellExecution gates a command;
#     preToolUse gates a write and blocks it BEFORE it lands, which afterFileEdit,
#     the event this file used to read, could never do.
#   - An ADVISE needs a context channel, and a gating event has none: Cursor
#     delivers user_message and agent_message only on a deny, so an advisory sent
#     there collapses into a plain allow. postToolUse's additional_context is the
#     channel, and it arrives after the call rather than before it. For the rules
#     that advise (generated files, a search the graph answers better) reporting
#     after the fact is the intended behavior on every host, and what Cursor shapes
#     is only which event carries it.
#
# So a judged call runs magus TWICE here: once to gate it, once to explain it. That
# is the price of the split, and it is why the activity trail carries two rows per
# call on this host and one everywhere else.
#
# Every call passes --agent-name cursor so the observation magus records says which
# host produced it. Cursor carries conversation_id on every hook and session_id on
# the session ones, so the session is attributable too; neither can change a verdict.
#
# Coverage declarations, machine-read by the host-parity gate - see the longer
# note in magus-guard-command.sh. Both surfaces now reach the model on both
# decisions, which is what moving the write gate to preToolUse and the advisory to
# postToolUse bought; the two lines are what says so.
# magus-guard-template: 12
# magus-guard-coverage: schema=1 host=cursor surface=command deny=model advise=model pass=none
# magus-guard-coverage: schema=1 host=cursor surface=path deny=model advise=model pass=none

# Prefer the workspace's own ./magus over PATH. A repository that builds magus, or pins a
# newer one than is installed, keeps its RULES in that binary - and an older PATH copy does
# not fail loudly when it lacks them. It does not recognize the config key that ARMS a rule,
# warns about an unknown field, and returns pass: silent non-enforcement at exit 0. Measured
# 2026-08-13, when a write into a declared notes store was allowed by a binary that predated
# the knowledge.notes key while `magus doctor` reported the guard as fine.
#
# Found by walking UP to the magusfile, not by testing ./magus alone. A hook runs in the
# host's session directory, and that is not always the workspace root: a session opened in
# a subdirectory, or opened in one checkout while the work happens in another, tests a
# ./magus that is not there and falls through to PATH. Where PATH's copy cannot load the
# workspace at all, that is the entire guard failing open - measured 2026-08-27, when a
# piped `magus affected ci` that the rules DO deny ran unjudged. Same upward search for a
# project root that every other ecosystem's runner does.
guard_root=$PWD
while [ -n "$guard_root" ] && [ -z "$GUARD_MAGUS_BIN" ]; do
  if [ -f "$guard_root/magusfile.buzz" ]; then
    [ -x "$guard_root/magus" ] && GUARD_MAGUS_BIN=$guard_root/magus
    break
  fi
  guard_root=${guard_root%/*}
done
[ -n "$GUARD_MAGUS_BIN" ] || GUARD_MAGUS_BIN=$(command -v magus 2>/dev/null)

# stdin is a pipe and drains once, so the event is read into a variable and every
# field is selected from that. `// empty` keeps a hook without a field at the empty
# string rather than at the literal "null".
event=$(cat)
event_name=$(printf '%s' "$event" | jq -r '.hook_event_name // empty' 2>/dev/null)
session=$(printf '%s' "$event" | jq -r '.session_id // .conversation_id // empty' 2>/dev/null)
transcript=$(printf '%s' "$event" | jq -r '.transcript_path // empty' 2>/dev/null)
shell_command=$(printf '%s' "$event" | jq -r '.command // empty' 2>/dev/null)
tool_command=$(printf '%s' "$event" | jq -r '.tool_input.command // empty' 2>/dev/null)
path=$(printf '%s' "$event" | jq -r '.tool_input.file_path // empty' 2>/dev/null)

# A payload naming no event is judged by SHAPE instead. Branching on the name is
# what lets one file serve five events, and a Cursor that stopped sending the field
# would otherwise take every arm below to the silent default, which is the one
# failure this guard cannot afford, since it looks exactly like a clean session.
if [ -z "$event_name" ]; then
  if [ -n "$shell_command" ]; then
    event_name=beforeShellExecution
  elif [ -n "$path" ]; then
    event_name=preToolUse
  fi
fi

# The two replies Cursor reads. A deny carries BOTH messages: user_message is shown
# to the person and agent_message reaches the model. Neither is delivered on an
# allow, which is why the advisory lives on a different event.
gate_template='{{if eq .decision "deny"}}{"permission":"deny","user_message":{{toJson .reason}},"agent_message":{{toJson .reason}}}{{else}}{"permission":"allow"}{{end}}'
advise_template='{{if eq .decision "advise"}}{"additional_context":{{toJson .context}}}{{else}}{}{{end}}'

# guard_notice_once succeeds the first time $1 fires in this session and fails on every
# repeat, so a caller writes `guard_notice_once <family> && printf ...`. See
# magus-guard-command.sh for the full reasoning; the short version is that these notices
# report a broken installation, which is a fact for the person with nothing in it an agent
# can act on, so a repeat is noise.
#
# The marker lives under TMPDIR because this runs when magus is missing or too broken to
# judge, so it cannot ask magus for anything. An event that reports no session shares a
# marker aged out after GUARD_NOTICE_WINDOW minutes rather than going quiet forever.
guard_notice_once() {
  notice_dir=${TMPDIR:-/tmp}/magus-guard-notices
  notice_key=$(printf '%s' "${session:-anon}" | cksum | cut -d' ' -f1)
  notice_marker=$notice_dir/$notice_key.$1
  mkdir -p "$notice_dir" 2>/dev/null || return 0
  if [ -f "$notice_marker" ]; then
    [ -n "$session" ] && return 1
    find "$notice_marker" -mmin +"${GUARD_NOTICE_WINDOW:-120}" 2>/dev/null | grep -q . || return 1
  fi
  : > "$notice_marker" 2>/dev/null
  return 0
}

# guard pipes its first argument into `magus session hook` with the rest as flags. The
# thing being judged goes in on STDIN, never in argv: a command is arbitrary text, and
# one passed as an argument is a quoting mistake away from being re-parsed.
guard() {
  guard_input=$1
  shift
  printf '%s' "$guard_input" | "$GUARD_MAGUS_BIN" session hook --agent-name cursor \
    --session "$session" --transcript "$transcript" "$@"
}

# guard_failure_notice states WHICH binary went silent, what version it is, and what it
# actually said - the three facts a reader otherwise spends a session collecting. It takes
# the same arguments the failed call did, and re-runs it to capture the stderr the verdict
# path discards: one extra process, only on the path that is already broken. WARN lines are
# dropped because a config the binary is too old to parse warns BEFORE it fails, and that
# warning is a symptom of the same staleness rather than the error.
#
# Held to one firing per session, and printed on stderr, which Cursor surfaces: a broken
# installation is a fact for the person, and there is nothing in it a model can act on.
guard_failure_notice() {
  guard_notice_once failed || return 0
  ver=$("$GUARD_MAGUS_BIN" version 2>/dev/null | head -n 1)
  [ -n "$ver" ] || ver='version unreadable'
  why=$(guard "$@" 2>&1 >/dev/null | grep -v 'WARN' | head -n 1)
  [ -n "$why" ] || why='it printed no error'
  printf 'magus guard is NOT running: %s (%s) could not judge this call, so its deny and advise rules are unenforced. It said: %s. Rebuild or update THAT binary to restore the guard.\n' \
    "$GUARD_MAGUS_BIN" "$ver" "$why" >&2
}

# One availability check for every arm. Cursor already fails open on a hook crash or
# malformed JSON unless the hook sets failClosed, so allowing here matches the
# surrounding contract rather than pretending to be stricter than it; for strict
# behavior, set failClosed on the hook and answer deny instead. What was missing was
# saying so: a silent fail-open is the one outcome nobody can tell from a guarded
# session. The gating events need an explicit allow, and the rest read an empty reply
# as no opinion.
if [ -z "$GUARD_MAGUS_BIN" ] || [ ! -x "$GUARD_MAGUS_BIN" ]; then
  guard_notice_once unavailable && printf '%s\n' "magus guard is NOT running: magus is not on PATH, so its deny and advise rules are unenforced right now. Install magus, or set GUARD_MAGUS_BIN to its path, to restore the guard." >&2
  case $event_name in
  beforeShellExecution | preToolUse | subagentStart)
    printf '%s' '{"permission":"allow"}'
    ;;
  esac
  exit 0
fi

# Every verdict below is captured and printed rather than piped straight through,
# because `magus session hook` exits non-zero on a deny and Cursor reads a non-zero
# hook as a CRASH - which it fails open on, unless failClosed is set. Letting that
# status escape would turn every block into an allow, silently, which is the one
# outcome worse than not installing the guard. Cursor's channel is the JSON on
# stdout, and this exits 0 so that JSON is what it acts on.
#
# An empty verdict is a BROKEN guard, never a pass: the templates above render a
# reply for every decision, so nothing but a magus that could not run leaves one
# empty - too old for `session hook`, unable to load the workspace, half-written by
# a concurrent build. Allowing is still right; announcing it is what was missing.
case $event_name in
sessionEnd)
  # Not a guard. It records the revision, branch and dirtiness of the tree when a
  # session ends, so whoever comes back reads `magus session` instead of
  # reconstructing where the work stopped. It prints nothing and judges nothing.
  "$GUARD_MAGUS_BIN" session checkpoint --agent-name cursor \
    --session "$session" --transcript "$transcript" >/dev/null 2>&1
  exit 0
  ;;
subagentStart)
  # Lease capture, reshaped rather than piped: magus recognizes a spawn by a
  # tool_input carrying a prompt, and Cursor spells the handed work `task` at the
  # top level. The parent is parent_conversation_id, since conversation_id here is
  # the child's. Nothing judges a lease: a prompt is prose, so the verdict is
  # always a pass, the output is discarded, and this arm always allows.
  printf '%s' "$event" | jq -c '{
      hook_event_name: (.hook_event_name // ""),
      session_id: (.parent_conversation_id // .conversation_id // ""),
      transcript_path: (.transcript_path // ""),
      tool_input: {prompt: (.task // ""), subagent_type: (.subagent_type // "")}
    }' 2>/dev/null | "$GUARD_MAGUS_BIN" session hook --agent-name cursor >/dev/null 2>&1
  printf '%s' '{"permission":"allow"}'
  exit 0
  ;;
postToolUse)
  # The advise channel, for whichever surface the payload names. It renders {} on
  # anything that is not an advise, so Cursor always gets a reply it can parse.
  if [ -n "$tool_command" ]; then
    verdict=$(guard "$tool_command" -o "template=$advise_template" 2>/dev/null)
    if [ -z "$verdict" ]; then
      guard_failure_notice "$tool_command"
      verdict='{}'
    fi
  elif [ -n "$path" ]; then
    verdict=$(guard "$path" --path -o "template=$advise_template" 2>/dev/null)
    if [ -z "$verdict" ]; then
      guard_failure_notice "$path" --path
      verdict='{}'
    fi
  else
    verdict='{}'
  fi
  printf '%s' "$verdict"
  exit 0
  ;;
preToolUse)
  # The write gate. Cursor's preToolUse fires for every tool, so this answers on the
  # SHAPE of the payload rather than on a tool name: a tool_input carrying a
  # file_path is a write. The shell tool reaches this hook too, carrying
  # tool_input.command, and is deliberately left to beforeShellExecution, because
  # judging it here as well would record two verdicts for one command.
  if [ -z "$path" ]; then
    printf '%s' '{"permission":"allow"}'
    exit 0
  fi
  verdict=$(guard "$path" --path -o "template=$gate_template" 2>/dev/null)
  if [ -z "$verdict" ]; then
    guard_failure_notice "$path" --path
    verdict='{"permission":"allow"}'
  fi
  ;;
beforeShellExecution)
  verdict=$(guard "$shell_command" -o "template=$gate_template" 2>/dev/null)
  if [ -z "$verdict" ]; then
    guard_failure_notice "$shell_command"
    verdict='{"permission":"allow"}'
  fi
  ;;
*)
  # An event this file does not serve. An empty reply is no opinion.
  exit 0
  ;;
esac

printf '%s' "$verdict"
exit 0
```

## Notifications

Cursor can run a command on its agent hook surface. Shape the event into the
canonical envelope and pipe it to `magus session notify`; see [Attention hooks](notifications.md).

## Recording where the work stands

The `sessionEnd` entry in the wiring above records the revision, branch and
dirtiness of the tree when a session ends; `magus session` lists it. The script
handles that arm itself, calling `magus session checkpoint` rather than the
shared [`magus-checkpoint.sh`](guard-templates.md#magus-checkpointsh), so this
host stays a one-file install; the shared template does the identical job if you
would rather point `sessionEnd` at it with `GUARD_AGENT_NAME=cursor`.

`sessionEnd` carries `session_id` and every hook carries `transcript_path`, so
both pointers are recorded. They are pointers magus stores and never opens, and a
checkpoint without them still says where the work sits, which is the part a person
coming back needs.

`magus session checkpoint --note "..."` writes the same record by hand.

## Lease capture

`subagentStart` fires when Cursor hands work to a sub-agent, and carries the
handed `task`, the `subagent_type`, and `parent_conversation_id` for the parent
side. That is everything magus records as a spawn, under different names, so the
script reshapes the payload into the canonical envelope before piping it: magus
recognizes a spawn by a `tool_input` carrying a `prompt`, never by a tool name it
would have to enumerate per host.

It records; it does not judge. A lease prompt is prose, so the verdict is always
a pass and the arm always allows. To join those events to a ledger, write the
marker line documented in [Any other host](any-host.md#lease-capture) at the top
of the prompt you hand the sub-agent.

This one is **unverified live**. An open Cursor forum report says
`subagentStart` and `subagentStop` never fire while `beforeShellExecution` from
the same `hooks.json` works normally. The wiring ships anyway, because a hook that
never fires costs nothing and a missing one cannot be found; check
`magus session` for `agent_spawn` events before relying on it.

## Coverage and limits

**Both surfaces now reach the model on both decisions.** That is new, and it cost
two events per judged call: the write gate moved from `afterFileEdit`, which
fires once the write has landed, to `preToolUse`, which blocks it; and the
advisory moved from stderr prose to `postToolUse.additional_context`. Reporting a
declared-output edit after the call is not a concession, since that rule only ever
explains, on every host. Reporting it to the PERSON was, and that is what changed.

**Judging twice is the price.** A gating event carries no message on an allow, so
the explanation has to come from a second event, and magus is asked about the same
call twice. The activity trail therefore carries two rows per judged call here.

**Post-compaction rehydration is not expressible.** Every other host has an event
that hands a compacted session its state back. Cursor's `preCompact` returns
`user_message` only, which reaches the person and not the model, and
`beforeSubmitPrompt` explicitly cannot inject context. So there is nothing to wire
and nothing is faked: run `magus session --brief` and paste it, or read it
yourself. `sessionStart.additional_context` does reach the model, but it fires
when nothing has been lost yet.

**A tool failure carries no hint.** `postToolUseFailure` has no response fields at
all, so the one place a host could explain a failing command is closed here.

Cursor fails open on a hook crash or malformed JSON unless the hook sets
`failClosed`. The script above matches that stance instead of pretending to be
stricter than the surrounding contract. For strict behavior, set `failClosed` on
the hook and change its missing-binary branch to a deny.

The magus half is verified: the verdict shape this script parses is checked
against this repository's binary. The Cursor half is written against the
product's published hook documentation and has not been executed here, so
confirm it against [Cursor hooks](https://cursor.com/docs/agent/hooks).

There is also no session-load recipe for this host, where the other three ship
one. Nothing in Cursor prevents it; nobody has written it.

## Verify

```sh
magus doctor
```

**guard binary** names the binary a hook would resolve; **guard wiring** runs a
canary command through it and checks that a host config invokes a template whose
version marker is current.
