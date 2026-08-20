---
title: Guard hook templates
description: The two POSIX sh templates Claude Code and Codex run for the magus guard - the variables that adapt them to a host, the version marker that tells you when your copy is stale, and the full source of each.
tags: [agents, guard, hooks, templates, claude code, codex]
---

# Guard hook templates

These are files, not snippets. They sit in
[`docs/guides/integrations/agents/`](https://github.com/egladman/magus/tree/main/docs/guides/integrations/agents):
download them from there, or copy a block below. magus's own repository invokes
the same files rather than keeping a private copy, so what it dogfoods is what
you get, and two tests fail if its config stops referencing them or a block here
drifts from the file.

They are a magus project of their own, so the TypeScript beside them is held to
the same gates as the rest of the workspace
(`magus run lint docs/guides/integrations/agents` runs `tsc --noEmit`, Biome,
and shellcheck).

Two hosts run these two files: [Claude Code](claude-code.md) and
[Codex](codex.md). [Cursor](cursor.md) and [OpenCode](opencode.md) each ship one
self-contained file instead, on their own pages, because a host that needs three
downloads to install a guard ends up without one.

## Checking whether your copy is current

Once you copy a template into your host's config it is yours, and magus cannot
reach it again. That is the point - you are meant to edit these - but it means a
fix magus makes never arrives on its own, and nothing about your copy says how
old it is. So each one carries a version line:

```sh
grep magus-guard-template ~/.claude/hooks/magus-guard-command.sh
```

Compare it with the version in the block below. If yours is lower or absent,
re-copy - and diff rather than overwrite, because your edits are worth keeping.
A missing line means the copy predates versioning entirely.

The version tracks BEHAVIOR, not wording: it moves when a template starts doing
something different, not when a comment is rewritten. It is deliberately not a
checksum, because these are yours to modify and a checksum would flag your own
edits as drift.

This is the one part of the agent surface with no automatic staleness check.
Installed skills are generated, so `magus doctor` regrades them against
the binary; a copied hook template is owned by you, and this line stands in for
that. `magus doctor`'s **guard wiring** check reads the marker in whatever file
your host config points at, and fails when it is stale or missing.

## How they fit together

One implementation per guard surface. A host sets overrides and delegates:

| variable                     | what it is                                                                                      |
| ---------------------------- | ----------------------------------------------------------------------------------------------- |
| `HOST_EVENT_PATH`            | dot-path to the command or file path inside your host's event JSON                              |
| `HOST_SESSION_PATH`          | dot-path to the session id inside your host's event JSON                                        |
| `HOST_RESPONSE`              | Go template rendering your host's reply from the verdict                                        |
| `GUARD_AGENT_NAME`           | the agent host name recorded on the activity event (`claude-code`, `codex`, ...)                |
| `GUARD_UNAVAILABLE_RESPONSE` | what to print when magus is missing, so each host picks its own fail-open or fail-closed stance |
| `GUARD_FAILED_RESPONSE`      | the same, for a magus that is found but cannot judge the input                                  |
| `GUARD_MAGUS_BIN`            | absolute path to magus when it is not on PATH                                                   |

`GUARD_AGENT_NAME` and `HOST_SESSION_PATH` feed `magus hook --agent-name` and
`--session`, which are pure attribution: they label the recorded observation and
cannot change a verdict. A host that supplies neither is judged identically and
simply records less about itself.

`GUARD_MAGUS_BIN` avoids the `MAGUS_*` prefix on purpose. That space is magus's
own configuration surface, and a variable these templates invent must not look
like a setting magus reads.

## `magus-guard-command.sh`

The command guard. Claude Code and Codex both run this one file; each sets its
overrides and execs it, so there is one implementation to reason about.

```sh
#!/usr/bin/env sh
# magus guard hook: judges ONE shell command an agent is about to run.
#
# This file is the source of truth. The docs site embeds it, magus's own
# repository invokes it, and you can download it and do the same. POSIX sh, no
# bashisms; nothing in it is magus-internal.
#
# Contract: reads the host's event as JSON on stdin, selects its command with
# jq, then pipes the command into magus hook. It writes the host's response on
# stdout and exits 0 either way. Override any of the variables below:
#
#   HOST_EVENT_PATH  dot-path to the command inside your host's event
#   HOST_SESSION_PATH  dot-path to the session id inside your host's event
#   HOST_TRANSCRIPT_PATH  dot-path to your host's own log of this session
#   HOST_RESPONSE    Go template rendering your host's reply
#   HOST_ADVISE_BRANCH  the advise arm of that template
#   GUARD_NO_ADVISE  set it when the host has no context-injection channel, so
#                    an advise renders nothing rather than something it rejects
#   GUARD_AGENT_NAME  the agent host name recorded alongside the observation
#   GUARD_MAGUS_BIN  path to the binary, when it is not on PATH
#   GUARD_UNAVAILABLE_RESPONSE  what to print when magus cannot be found, so a
#                    host can choose its own fail-open or fail-closed stance
#   GUARD_FAILED_RESPONSE  the same, for a magus that IS found but cannot judge
#                    the command (too old for `hook`, cannot load the workspace)
#
# The defaults are Claude Code's event and response shape.
#
# GUARD_AGENT_NAME and the session are ATTRIBUTION, not policy. magus records them on
# its activity event so a reader can tell which host produced an observation;
# neither one can change the verdict, and a host whose event carries no session
# id records none and is judged exactly the same.
#
# GUARD_MAGUS_BIN is deliberately NOT called MAGUS_BIN: the whole MAGUS_* space is
# magus's own configuration surface, so a variable this template invents must stay
# out of it rather than look like a setting magus reads.
#
# On a missing magus this prints a visible notice rather than exiting quietly. A
# bare `command -v magus || exit 0` fails SILENTLY - the guard never runs and
# nothing says so - and an unguarded session you know about beats one you do not.
#
# The line below declares, per guard surface, how much of a verdict this file
# can carry: model (reaches the agent), human (reaches the person only), or none
# (not delivered). It is machine-read by the host-parity gate, which fails the
# build when a decision or surface exists in the guard contract that some host
# was never asked about. Keep it true to what HOST_RESPONSE actually renders.
# magus-guard-template: 7
# magus-guard-coverage: schema=1 host=claude-code surface=command deny=model advise=model pass=none
# magus-guard-coverage: schema=1 host=codex surface=command deny=model advise=none pass=none

# Plain assignment, NOT ${VAR:=default}: the response template is full of `}` and
# the first one would terminate a ${...} expansion, silently truncating it.
[ -n "$HOST_EVENT_PATH" ] || HOST_EVENT_PATH='tool_input.command'
[ -n "$HOST_SESSION_PATH" ] || HOST_SESSION_PATH='session_id'
[ -n "$HOST_TRANSCRIPT_PATH" ] || HOST_TRANSCRIPT_PATH='transcript_path'
[ -n "$GUARD_AGENT_NAME" ] || GUARD_AGENT_NAME='claude-code'
# The advise arm is split out because not every host has one. Codex's PreToolUse
# REJECTS additionalContext - it treats the key as an error and the hook then fails
# OPEN - so an advisory sent there is not merely dropped, it disarms the guard for
# that call. Codex sets GUARD_NO_ADVISE from codex-hooks.json and declares advise=none.
# A plain `[ -n ... ] ||` cannot express "deliberately empty" - an empty value looks
# unset and gets the default back - and ${VAR-default} is unusable here for the same
# `}` reason as above. So the suppression is its own flag.
if [ -n "$GUARD_NO_ADVISE" ]; then
  HOST_ADVISE_BRANCH=''
else
  [ -n "$HOST_ADVISE_BRANCH" ] || HOST_ADVISE_BRANCH='{{else if eq .decision "advise"}}{"hookSpecificOutput":{"hookEventName":"PreToolUse","additionalContext":{{toJson .context}}}}'
fi
[ -n "$HOST_RESPONSE" ] || HOST_RESPONSE='{{if eq .decision "deny"}}{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":{{toJson .reason}}}}'"$HOST_ADVISE_BRANCH"'{{end}}'
# Prefer the workspace's own ./magus over PATH. A repository that builds magus, or pins a
# newer one than is installed, keeps its RULES in that binary - and an older PATH copy does
# not fail loudly when it lacks them. It does not recognize the config key that ARMS a rule,
# warns about an unknown field, and returns pass: silent non-enforcement at exit 0. Measured
# 2026-08-13, when a write into a declared notes store was allowed by a binary that predated
# the knowledge.notes key while `magus doctor` reported the guard as fine.
[ -n "$GUARD_MAGUS_BIN" ] || { [ -x ./magus ] && GUARD_MAGUS_BIN=./magus; }
[ -n "$GUARD_MAGUS_BIN" ] || GUARD_MAGUS_BIN=$(command -v magus 2>/dev/null)
[ -n "$GUARD_UNAVAILABLE_RESPONSE" ] || GUARD_UNAVAILABLE_RESPONSE='{"hookSpecificOutput":{"hookEventName":"PreToolUse","additionalContext":"magus guard is NOT running: magus is not on PATH, so its deny and advise rules are unenforced right now. Install magus, or set GUARD_MAGUS_BIN to its path, to restore the guard."}}'
[ -n "$GUARD_FAILED_RESPONSE" ] || GUARD_FAILED_RESPONSE='{"hookSpecificOutput":{"hookEventName":"PreToolUse","additionalContext":"magus guard is NOT running: the magus binary was found but could not judge this command, so its deny and advise rules are unenforced right now. It is probably too old for the hook subcommand, or cannot load this workspace - run magus hook by hand to see the error, then rebuild or update it to restore the guard."}}'

if [ -z "$GUARD_MAGUS_BIN" ] || [ ! -x "$GUARD_MAGUS_BIN" ]; then
  printf '%s' "$GUARD_UNAVAILABLE_RESPONSE"
  exit 0
fi

# stdin is a pipe and can only be drained once, so the event is read into a
# variable and selected from twice - the command to judge, and the session id to
# attribute it to. `// empty` keeps a host without that field at the empty
# string rather than the literal "null".
event=$(cat)
session=$(printf '%s' "$event" | jq -r ".$HOST_SESSION_PATH // empty")
transcript=$(printf '%s' "$event" | jq -r ".$HOST_TRANSCRIPT_PATH // empty")

# Attribution is BEST EFFORT; the verdict is not.
#
# --agent-name and --session postdate the current magus release, and this template is downloaded and run
# against whatever binary a reader already has. Passing them unconditionally does not degrade the
# guard, it BREAKS it: an older binary rejects the unknown flag, prints its usage to stdout, and
# exits non-zero, so the host receives no verdict at all and every deny and advise rule silently
# stops being enforced. A guard that fails because of a metadata flag has its priorities backwards.
#
# So: try with attribution, and on any failure re-run without it - exactly the call this script made
# before attribution existed. One extra process only on an older binary, and none once the flags are
# in a release.
guard() {
  printf '%s' "$event" | jq -r ".$HOST_EVENT_PATH" | "$GUARD_MAGUS_BIN" hook "$@" -o "template=$HOST_RESPONSE"
}
# A DENY exits non-zero (2) with the verdict on stdout, so a bare `||` retry would treat
# every blocked command as "this binary rejected the attribution flags" and judge it a
# second time - unattributed, and recorded twice in the activity trail. Emptiness alone
# cannot tell the cases apart either, because a pass renders empty on purpose. Both
# together can: a rejected flag prints its usage to STDERR and leaves stdout empty, while
# any real verdict that is not a pass leaves something on stdout.
verdict=$(guard --agent-name "$GUARD_AGENT_NAME" --session "$session" --transcript "$transcript" 2>/dev/null)
status=$?
if [ "$status" -ne 0 ] && [ -z "$verdict" ]; then
  verdict=$(guard 2>/dev/null)
  status=$?
fi

# A PASS and a BROKEN GUARD both render nothing, and telling them apart is the whole
# point of this block. A pass exits 0 with empty output because there was nothing to
# say; a binary that cannot run - too old for `hook`, unable to load the workspace,
# half-written by a concurrent build - exits non-zero with empty output, and printing
# that as a pass silently disables every rule with nothing anywhere saying so.
#
# That silence is the failure mode this guard can least afford, because it looks
# exactly like a clean session. The same reasoning is already spelled out above for a
# MISSING binary; a broken one had been left to fail quietly, which is the case that
# actually occurs - a stale binary on PATH outlives a missing one.
#
# Fail OPEN either way. A guard that blocks work because it cannot judge it has its
# priorities backwards, and an unguarded session you know about beats one you do not.
if [ "$status" -ne 0 ] && [ -z "$verdict" ]; then
  printf '%s' "$GUARD_FAILED_RESPONSE"
  exit 0
fi
printf '%s' "$verdict"
```

## `magus-guard-path.sh`

The declared-output guard. Wire it to your host's file-editing tool rather than
its shell tool. It explains rather than blocks: editing a generated file is
wasteful, not destructive.

```sh
#!/usr/bin/env sh
# magus guard hook: judges ONE file path an agent is about to write.
#
# Companion to magus-guard-command.sh, wired to your host's file-editing tool
# rather than its shell tool. POSIX sh, no bashisms.
#
# The declared-output rule here is the one guard rule that is not a heuristic:
# magus reads every target's DECLARED outputs, so a generated file is generated
# by definition and an edit to it would be overwritten by the next run.
#
# That rule ADVISES rather than blocks. magus denies only what cannot be undone;
# a hand-edited generated file is wasteful, not destructive, since regenerating
# erases it. So it explains that the edit will be overwritten and lets the agent
# correct itself, rather than treating it as unable to learn. Every rule on this
# surface says nothing on any uncertainty - no magus, no workspace, an unclaimed
# path - because an advisory fired on a guess trains the reader to ignore it.
#
# HOST_RESPONSE renders BOTH arms even though the rules shipping today only
# advise. That is deliberate and it is why this template exists at version 2.
# These files are COPIED into a reader's config and never self-correct, so a
# deny arm added at the same time as the first denying rule would fail OPEN on
# every already-installed copy: the deny renders empty, magus exits non-zero,
# and the tail below reads empty-output-plus-nonzero as a broken guard and exits
# 0, which every host takes as allow. Shipping the arm first gives installed
# copies a window to update against a rule that is not yet firing.
#
# A host with no file-write hook still gets the command rules; it just misses
# this one. That is a coverage difference to record, not a reason to skip it.
#
# GUARD_AGENT_NAME and HOST_SESSION_PATH work exactly as they do in
# magus-guard-command.sh: attribution recorded on the activity event, never an
# input to the verdict.
#
# Coverage declaration, machine-read by the host-parity gate - see the longer
# note in magus-guard-command.sh. It records what HOST_RESPONSE RENDERS, not
# which rules currently fire, so deny=model is true the moment the arm exists.
# magus-guard-template: 7
# magus-guard-coverage: schema=1 host=claude-code surface=path deny=model advise=model pass=none
# magus-guard-coverage: schema=1 host=codex surface=path deny=model advise=none pass=none

# Plain assignment, NOT ${VAR:=default}: the response template is full of `}`
# and the first one would terminate a ${...} expansion.
[ -n "$HOST_EVENT_PATH" ] || HOST_EVENT_PATH='tool_input.file_path'
[ -n "$HOST_SESSION_PATH" ] || HOST_SESSION_PATH='session_id'
[ -n "$HOST_TRANSCRIPT_PATH" ] || HOST_TRANSCRIPT_PATH='transcript_path'
[ -n "$GUARD_AGENT_NAME" ] || GUARD_AGENT_NAME='claude-code'
# Same split, and the same reason, as in magus-guard-command.sh: a host whose
# PreToolUse rejects additionalContext fails OPEN on one, so an advisory it cannot
# take disarms the call rather than merely going unread. Codex wires BOTH surfaces
# to PreToolUse, so it suppresses here too.
if [ -n "$GUARD_NO_ADVISE" ]; then
  HOST_ADVISE_BRANCH=''
else
  [ -n "$HOST_ADVISE_BRANCH" ] || HOST_ADVISE_BRANCH='{{else if eq .decision "advise"}}{"hookSpecificOutput":{"hookEventName":"PreToolUse","additionalContext":{{toJson .context}}}}'
fi
[ -n "$HOST_RESPONSE" ] || HOST_RESPONSE='{{if eq .decision "deny"}}{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":{{toJson .reason}}}}'"$HOST_ADVISE_BRANCH"'{{end}}'
# Prefer the workspace's own ./magus over PATH. A repository that builds magus, or pins a
# newer one than is installed, keeps its RULES in that binary - and an older PATH copy does
# not fail loudly when it lacks them. It does not recognize the config key that ARMS a rule,
# warns about an unknown field, and returns pass: silent non-enforcement at exit 0. Measured
# 2026-08-13, when a write into a declared notes store was allowed by a binary that predated
# the knowledge.notes key while `magus doctor` reported the guard as fine.
[ -n "$GUARD_MAGUS_BIN" ] || { [ -x ./magus ] && GUARD_MAGUS_BIN=./magus; }
[ -n "$GUARD_MAGUS_BIN" ] || GUARD_MAGUS_BIN=$(command -v magus 2>/dev/null)

if [ -z "$GUARD_MAGUS_BIN" ] || [ ! -x "$GUARD_MAGUS_BIN" ]; then
  # Prints nothing by default: for most hosts an empty response means "allow".
  # Set GUARD_UNAVAILABLE_RESPONSE for a host that needs an explicit verdict.
  [ -n "$GUARD_UNAVAILABLE_RESPONSE" ] && printf '%s' "$GUARD_UNAVAILABLE_RESPONSE"
  exit 0
fi

# One drain of stdin, two selections from it: the path to judge, and the session
# id to attribute it to. `// empty` keeps a host without that field at the empty
# string rather than the literal "null".
event=$(cat)
session=$(printf '%s' "$event" | jq -r ".$HOST_SESSION_PATH // empty")
transcript=$(printf '%s' "$event" | jq -r ".$HOST_TRANSCRIPT_PATH // empty")

# Attribution is BEST EFFORT; the verdict is not. --agent-name and --session postdate the current magus
# release, and an older binary rejects the unknown flag outright - printing usage to stdout and
# exiting non-zero - which leaves the host with no verdict rather than an unattributed one. Try with
# attribution, fall back to the call this script made before it existed.
guard() {
  printf '%s' "$event" | jq -r ".$HOST_EVENT_PATH" | "$GUARD_MAGUS_BIN" hook --path "$@" -o "template=$HOST_RESPONSE"
}
# Same discrimination as magus-guard-command.sh, and for the same reason now that this
# surface can render a deny: a DENY exits non-zero (2) with the verdict on stdout, so a
# bare `||` retry would treat every blocked write as "this binary rejected the attribution
# flags" and judge it a second time - unattributed, and recorded twice in the activity
# trail. Emptiness alone cannot tell the cases apart either, because a pass renders empty
# on purpose. Both together can: a rejected flag prints its usage to STDERR and leaves
# stdout empty, while any real verdict that is not a pass leaves something on stdout.
verdict=$(guard --agent-name "$GUARD_AGENT_NAME" --session "$session" --transcript "$transcript" 2>/dev/null)
status=$?
if [ "$status" -ne 0 ] && [ -z "$verdict" ]; then
  verdict=$(guard 2>/dev/null)
  status=$?
fi

# A pass and a broken guard both render nothing; see magus-guard-command.sh for why
# telling them apart matters. Kept identical here so neither surface grows a behavior
# the other lacks - the difference is only that this one has no default message,
# because for most hosts an empty response on this surface already means "allow".
if [ "$status" -ne 0 ] && [ -z "$verdict" ]; then
  [ -n "$GUARD_FAILED_RESPONSE" ] && printf '%s' "$GUARD_FAILED_RESPONSE"
  exit 0
fi
printf '%s' "$verdict"
```

## `magus-guard-observe.sh`

The one template that carries no verdict. Wire it to the tools that only LOOK -
your host's read equivalent - and it records the path the agent reached without
judging it. It prints nothing and always exits 0.

Do not point a read tool at `magus-guard-path.sh` instead. A read event carries
a file path just as a write event does, so the write rules would advise "you are
editing a declared output" at a file the agent merely opened. `--observe` is
what separates the two, and only this wrapper can set it, because only it knows
which of your host's tools look.

It declares no `magus-guard-coverage` line, and that absence is deliberate: a
coverage declaration states how much of a verdict a host can carry on a guard
surface, and this file carries no verdict on no surface.

```sh
#!/usr/bin/env sh
# magus observe hook: records ONE path an agent reached, and judges nothing.
#
# This file is the source of truth. The docs site embeds it, magus's own
# repository invokes it, and you can download it and do the same. POSIX sh, no
# bashisms; nothing in it is magus-internal.
#
# Wire it to the tools that only LOOK - your host's read equivalent. The guard
# templates beside this one handle the tools that ACT. Do not point a read tool
# at those: a read event carries a file path, so the write rules would advise
# "you are editing a declared output" at a file the agent merely opened.
# --observe is what separates the two, and only this wrapper can set it,
# because only it knows which of your host's tools look.
#
# Contract: reads the host's event as JSON on stdin, selects the path with jq,
# and pipes it into `magus hook --observe`. It prints NOTHING and always exits
# 0 - see the note on that below, which is load-bearing rather than tidy.
# Override any of the variables below:
#
#   HOST_EVENT_PATH  dot-path to the read path inside your host's event
#   HOST_SESSION_PATH  dot-path to the session id inside your host's event
#   HOST_TRANSCRIPT_PATH  dot-path to your host's own log of this session
#   GUARD_AGENT_NAME  the agent host name recorded alongside the observation
#   GUARD_MAGUS_BIN  path to the binary, when it is not on PATH
#
# The defaults are Claude Code's event shape, matching its two siblings. A
# different host overrides the dot-paths and passes its own GUARD_AGENT_NAME,
# exactly as codex-hooks.json already does for the guard templates.
#
# NO magus-guard-coverage line, and that absence is deliberate rather than an
# oversight: a coverage declaration states how much of a VERDICT a host can
# carry on a guard surface, and this file carries no verdict on no surface. It
# never denies, never advises, and cannot change what your host does next. The
# parity gates ask that question only of artifacts that answer it.
#
# magus-guard-template: 7

# NO `set -e`, deliberately, and neither sibling uses it either.
#
# Under `set -e` a jq that is missing (127) or handed a payload it cannot parse
# aborts this script mid-way with jq's own message on stderr - on EVERY read.
# Worse, a PreToolUse hook's exit status is not advisory on every host: some
# treat a specific non-zero code as "block this tool call", so a malformed event
# could stop the agent from reading anything at all. An optional record must
# never be able to do that, so every failure below is swallowed and the script
# ends at `exit 0` on all paths.

[ -n "$HOST_EVENT_PATH" ] || HOST_EVENT_PATH='tool_input.file_path'
[ -n "$HOST_SESSION_PATH" ] || HOST_SESSION_PATH='session_id'
[ -n "$HOST_TRANSCRIPT_PATH" ] || HOST_TRANSCRIPT_PATH='transcript_path'
[ -n "$GUARD_AGENT_NAME" ] || GUARD_AGENT_NAME='claude-code'
# Prefer the workspace's own ./magus over PATH, for the same reason its two siblings do - and
# this file needs it MORE than they do, because it is silent by design. An older PATH copy
# does not know --observe at all: it rejects the flag, prints its usage to a stream this
# script discards, and exits non-zero into an `|| true` - so the observation is simply never
# recorded, forever, with nothing anywhere saying so. Measured 2026-08-14 in magus's own
# repository, where the wiring was correct, the binary was wrong, and the trail held 3252
# events and not one read.
[ -n "$GUARD_MAGUS_BIN" ] || { [ -x ./magus ] && GUARD_MAGUS_BIN=./magus; }
[ -n "$GUARD_MAGUS_BIN" ] || GUARD_MAGUS_BIN=$(command -v magus 2>/dev/null)

# An absent observer is SILENT, where an absent guard is loud.
#
# The guard templates announce themselves when magus cannot be found, because an
# unenforced deny rule is a safety fact the reader needs. Nothing is unenforced
# here - there is no rule - so the same announcement would be a per-read
# interruption reporting that an optional record was not written.
if [ -z "$GUARD_MAGUS_BIN" ] || [ ! -x "$GUARD_MAGUS_BIN" ]; then
  exit 0
fi

# stdin is a pipe and can only be drained once, so the event is read into a
# variable and selected from more than once. `// empty` keeps a host without one
# of these fields at the empty string rather than the literal "null", and jq's
# stderr is discarded because a payload this script cannot parse is a record it
# will not write, not news for the person trying to read a file.
#
# The path is extracted rather than piping the whole event through, because a
# payload magus does not recognize as an envelope is judged as the literal text
# it is - and for a search that carries a pattern but no path, that would record
# the entire event, query text included, as the thing the agent reached.
event=$(cat)
path=$(printf '%s' "$event" | jq -r ".$HOST_EVENT_PATH // empty" 2>/dev/null)

# Nothing to record is not a failure: a host event that names no path has no
# reach to report, and inventing one would claim a file the host never named.
# This gate comes BEFORE the remaining selections so the common no-op case - any
# tool whose event carries no read path - costs one jq rather than three.
[ -n "$path" ] || exit 0

session=$(printf '%s' "$event" | jq -r ".$HOST_SESSION_PATH // empty" 2>/dev/null)
transcript=$(printf '%s' "$event" | jq -r ".$HOST_TRANSCRIPT_PATH // empty" 2>/dev/null)

# A magus too old for --observe rejects the flag and exits non-zero. That is a
# real state worth knowing about once, but not once per read, so it is reported
# through the trail's own absence rather than through the session: if reads are
# missing from `magus activity`, the binary is too old. Both streams are
# discarded because a flag-parse error would otherwise reach the host as this
# hook's response on every read.
printf '%s' "$path" | "$GUARD_MAGUS_BIN" hook --observe \
  --agent-name "$GUARD_AGENT_NAME" --session "$session" --transcript "$transcript" \
  --event PreToolUse >/dev/null 2>&1

exit 0
```

## What a template must not get wrong

Three failure modes are worth naming, because each one looks like a working
guard.

**A missing arm renders empty.** `HOST_RESPONSE` carries both a deny arm and an
advise arm. A template missing one does not fail loudly; it renders nothing, and
every host reads nothing as allow. Claude Code's `--path` wiring once rendered
only the deny arm, so every advisory it produced was silently dropped, while the
shipped `magus-guard-path.sh` had the opposite gap and dropped denials.

**A pass and a broken guard both render nothing.** A pass exits 0 with empty
output because there was nothing to say. A binary that cannot run - too old for
`hook`, unable to load the workspace, half-written by a concurrent build - exits
non-zero with empty output. Printing that as a pass disables every rule with
nothing anywhere saying so. Both scripts discriminate on status and emptiness
together, and print `GUARD_FAILED_RESPONSE` for the second case.

**Attribution must never break a verdict.** `--agent-name` and `--session`
postdate the current release, and an older binary rejects an unknown flag by
printing usage and exiting non-zero, which leaves the host with no verdict at
all. Both scripts try with attribution and retry without it, and they retry only
when the call produced no verdict - never merely because it exited non-zero,
since a deny exits 2 with the verdict on stdout.

## Trying a verdict by hand

```sh
printf '%s' 'git stash' | magus hook -o name
printf '%s' 'MAGUS.md' | magus hook --path -o name
magus hook -o template
```

The last one lists the fields available to `-o template`. A deny exits 2 with
the verdict on stdout; a pass and an advise exit 0. See [The guard](guard.md)
for the rules behind the verdicts.
