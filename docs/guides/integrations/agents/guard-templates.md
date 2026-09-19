---
title: Guard hook templates
description: The hook templates Claude Code and Codex run for the magus guard, the checkpoint and the post-compaction brief, in POSIX sh and in Buzz - the variables that adapt them to a host, the version marker that tells you when your copy is stale, and the full source of each.
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

Two hosts run these files: [Claude Code](claude-code.md) and [Codex](codex.md).
Claude Code's harness wires the Buzz ports below; Codex wires the sh copies.
The two forms are one guard, and an executed case refuses a difference between them.
Harness apply merges opaque fragments that already name the scripts; Magus does
not inject a reserved command. [Cursor](cursor.md) and [OpenCode](opencode.md)
each ship one self-contained file instead, on their own pages, because a host
that needs five downloads to install a guard ends up without one.

## Checking whether your copy is current

Once you copy a template into your host's config it is yours, and magus cannot
reach it again. That is the point - you are meant to edit these - but it means a
fix magus makes never arrives on its own, and nothing about your copy says how
old it is. So each one carries a version line:

```sh
grep magus-guard-template ~/.claude/hooks/magus-command.sh
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

| variable                       | what it is                                                                                                                                                                                                                                   |
| ------------------------------ | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `HOST_EVENT_PATH`              | dot-path to the command or file path inside your host's event JSON                                                                                                                                                                           |
| `HOST_EVENT_RAW`               | (the `.sh` copies only) hand the whole event instead of one `HOST_EVENT_PATH` field - for a surface like MCP whose payload is a tool name plus a params object, not one string. The `.buzz` ports read this off the event instead; see below |
| `HOST_SESSION_PATH`            | dot-path to the session id inside your host's event JSON                                                                                                                                                                                     |
| `HOST_RESPONSE`                | Go template rendering your host's reply from the verdict                                                                                                                                                                                     |
| `__MAGUS_AGENT_NAME`           | the agent host name recorded on the activity event (`claude-code`, `codex`, ...)                                                                                                                                                             |
| `__MAGUS_UNAVAILABLE_RESPONSE` | what to print when magus is missing, so each host picks its own fail-open or fail-closed stance                                                                                                                                              |
| `__MAGUS_FAILED_RESPONSE`      | the same, for a magus that is found but cannot judge the input; unset, the notice is built from evidence                                                                                                                                     |
| `__MAGUS_BIN`                  | absolute path to magus when it is not on PATH                                                                                                                                                                                                |

`__MAGUS_AGENT_NAME` and `HOST_SESSION_PATH` feed `magus session hook --agent-name` and
`--session`, which are pure attribution: they label the recorded observation and
cannot change a verdict. A host that supplies neither is judged identically and
simply records less about itself.

`__MAGUS_BIN` avoids the `MAGUS_*` prefix on purpose. That space is magus's
own configuration surface, and a variable these templates invent must not look
like a setting magus reads.

### Why the two forms differ on knobs

Each surface ships twice: a POSIX `sh` copy and a `.buzz` port that renders the same
replies. The table above is the `sh` contract in full, because `sh` is what runs those
copies and `sh` is what reads a variable.

A `.buzz` port is wired differently. Its hook command is a plain argv - the host splits
it and runs `magus buzz` itself, with no shell anywhere - so a `VAR=value` prefix is a
word the host would look for a program named after, not an assignment. The two knobs
that vary PER ENTRY move accordingly:

- `HOST_EVENT_RAW` is gone. `magus-command.buzz` works out from the event whether to
  forward the whole envelope: it selects one field only when the tool is not in the
  `mcp__` namespace AND `HOST_EVENT_PATH` finds a string there. Everything else goes
  whole, because magus's own decoder knows every payload shape it reads and answers that
  there is nothing to judge for the rest, while a field selected out of an unrecognized
  shape gets judged as a command line it never was.
- `__MAGUS_SHELL_FLAGS` becomes argv: `magus buzz -s <file> -- --observes-skill-loads`.
  `magus buzz` forwards everything after `--` to the script, which parses it against the
  flags it publishes in `SUPPORTED_FLAGS`. An argument it does not know is named on stderr
  and left out, and the call is judged anyway - your host shows that line as a hook error.
  Nothing is dropped quietly: a guard running with flags nobody chose is the failure this
  shape exists to avoid, and so is a guard that refuses to answer because an entry was
  typed wrong.

The host-level variables in the table - `HOST_RESPONSE`, `__MAGUS_AGENT_NAME`,
`__MAGUS_BIN`, the two notice overrides - are unchanged in both forms. A host sets those
once; they do not distinguish one entry from another.

## `magus-command.sh`

The command guard, in POSIX sh. Codex runs this file; Claude Code runs the Buzz
port of it below. Each host sets its overrides and execs one of the two, so there
is one implementation to reason about and two ways to run it.

```sh
#!/usr/bin/env sh
# magus guard hook: judges ONE shell command an agent is about to run.
#
# This file is the source of truth. The docs site embeds it, magus's own
# repository invokes it, and you can download it and do the same. POSIX sh, no
# bashisms; nothing in it is magus-internal.
#
# Contract: reads the host's event as JSON on stdin, selects its command with
# jq, then pipes the command into magus session hook. It writes the host's response on
# stdout and exits 0 either way. Override any of the variables below:
#
#   HOST_EVENT_PATH  dot-path to the command inside your host's event
#   HOST_EVENT_RAW   when set, hand the WHOLE host event to magus session hook
#                    instead of selecting HOST_EVENT_PATH out of it - for a
#                    surface whose payload is not one string, such as an MCP
#                    tool call (a tool name plus a params object)
#   HOST_SESSION_PATH  dot-path to the session id inside your host's event
#   HOST_TRANSCRIPT_PATH  dot-path to your host's own log of this session
#   HOST_RESPONSE    Go template rendering your host's reply
#   HOST_ADVISE_BRANCH  the advise arm of that template
#   HOST_ASK_BRANCH  the ask arm of that template: the reply that puts the call in
#                    front of the PERSON through the host's own approval prompt
#   __MAGUS_NO_ADVISE  set it when the host has no context-injection channel, so
#                    an advise renders nothing rather than a reply it rejects
#   __MAGUS_AGENT_NAME  the agent host name recorded alongside the observation
#   __MAGUS_SHELL_FLAGS  extra `magus shell` flags this wiring declares about itself,
#                    space-separated. Capabilities, not policy: a config that also
#                    matches its host's skill tool passes --observes-skill-loads, and
#                    rules that require a skill load stand down where it is absent
#   __MAGUS_BIN  path to the binary, when it is not on PATH
#   __MAGUS_UNAVAILABLE_RESPONSE  what to print when magus cannot be found, so a
#                    host can choose its own fail-open or fail-closed stance
#   __MAGUS_FAILED_RESPONSE  the same, for a magus that IS found but cannot judge
#                    the command. Left unset, this file builds one from evidence:
#                    which binary it resolved, that binary's version, and the
#                    error it actually printed
#
# The defaults are Claude Code's event and response shape.
#
# __MAGUS_AGENT_NAME and the session are ATTRIBUTION, not policy. magus records them on
# its activity event so a reader can tell which host produced an observation;
# neither one can change the verdict, and a host whose event carries no session
# id records none and is judged exactly the same.
#
# __MAGUS_BIN is deliberately NOT called MAGUS_BIN: the whole MAGUS_* space is
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
#
# An ask reaches the person on both hosts, by different routes. Claude Code takes
# permissionDecision "ask" from PreToolUse and prompts. Codex parses that value and does
# not support it: the hook run is marked failed and the call CONTINUES, so on Codex this
# file never emits it. There the prompt comes from a rules file the codex harness writes
# (.codex/rules/magus.rules, a prefix_rule on git push with decision "prompt"), and the
# PermissionRequest event Codex raises before that prompt reaches this same file, which
# answers allow for a push the gate covers, leaves an ungated one to the person, and
# denies a leased worker's. Where Codex cannot prompt at all (no rules file, a
# permission_mode that never asks, a call no rule matches) the ask renders as a deny that
# names the person's own terminal.
# magus-guard-template: 16
# magus-guard-coverage: schema=1 host=claude-code surface=command deny=model advise=model pass=none ask=human
# magus-guard-coverage: schema=1 host=codex surface=command deny=model advise=model pass=none ask=human
# magus-guard-coverage: schema=1 host=claude-code surface=mcp deny=model advise=model pass=none ask=human
# claude-code's mcp row is real: an mcp__magus__* PreToolUse call carries no tool_input.command,
# so HOST_EVENT_RAW forwards the whole event instead, and the same hookSpecificOutput reply this
# file already renders for the command surface carries a deny or an advise on this one too.
# magus-guard-coverage: schema=1 host=codex surface=mcp deny=model advise=model pass=none ask=model
# Codex PreToolUse matches canonical MCP names such as mcp__magus__*, whose tool_input is
# an arguments object rather than a command string. The Codex template therefore sets
# HOST_EVENT_RAW=1 so magus session hook can judge the complete event and render the same
# hookSpecificOutput deny or advisory as it does for Bash. No rule prompts for an MCP call
# on Codex, so its ask renders as a deny.

# Plain assignment, NOT ${VAR:=default}: the response template is full of `}` and
# the first one would terminate a ${...} expansion, silently truncating it.
[ -n "$HOST_EVENT_PATH" ] || HOST_EVENT_PATH='tool_input.command'
[ -n "$HOST_SESSION_PATH" ] || HOST_SESSION_PATH='session_id'
[ -n "$HOST_TRANSCRIPT_PATH" ] || HOST_TRANSCRIPT_PATH='transcript_path'
[ -n "$__MAGUS_AGENT_NAME" ] || __MAGUS_AGENT_NAME='claude-code'
# The one arm here that does not fail open; see the truncated-envelope check below. Plain
# assignment for the same reason as the rest: a `}` would end a ${...}.
[ -n "$__MAGUS_UNREADABLE_RESPONSE" ] || __MAGUS_UNREADABLE_RESPONSE='{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":"magus guard could not read this call from the host, so nothing was judged. A payload that arrives truncated reads exactly like an empty one, which is why this is blocked rather than cleared. Retry the call."}}'
# The advise arm is split out because not every host has one, and because a host
# that REJECTS the key is worse off than one that ignores it: an unsupported field
# can make the host mark the hook run failed and continue the call, so an advisory
# it cannot take disarms the guard rather than merely going unread. No host shipped
# here is in that position today, since both hosts wired to this file take
# additionalContext, so the flag has no user and is kept for the one you may wire.
# A plain `[ -n ... ] ||` cannot express "deliberately empty" - an empty value looks
# unset and gets the default back - and ${VAR-default} is unusable here for the same
# `}` reason as above. So the suppression is its own flag.
if [ -n "$__MAGUS_NO_ADVISE" ]; then
  HOST_ADVISE_BRANCH=''
else
  [ -n "$HOST_ADVISE_BRANCH" ] || HOST_ADVISE_BRANCH='{{else if eq .decision "advise"}}{"hookSpecificOutput":{"hookEventName":"PreToolUse","additionalContext":{{toJson .context}}}}'
fi
# HOST_RESPONSE's default is assembled below, once the event is read: the ask arm depends on
# which event arrived and on what the host reports about its own approval settings.
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
while [ -n "$guard_root" ] && [ -z "$__MAGUS_BIN" ]; do
  if [ -f "$guard_root/magusfile.buzz" ]; then
    [ -x "$guard_root/magus" ] && __MAGUS_BIN=$guard_root/magus
    break
  fi
  guard_root=${guard_root%/*}
done
[ -n "$__MAGUS_BIN" ] || __MAGUS_BIN=$(command -v magus 2>/dev/null)
[ -n "$__MAGUS_UNAVAILABLE_RESPONSE" ] || __MAGUS_UNAVAILABLE_RESPONSE='{"hookSpecificOutput":{"hookEventName":"PreToolUse","additionalContext":"magus guard is NOT running: magus is not on PATH, so its deny and advise rules are unenforced right now. Install magus, or set __MAGUS_BIN to its path, to restore the guard."}}'

# stdin is a pipe and can only be drained once, so the event is read into a
# variable and selected from twice - the command to judge, and the session id to
# attribute it to. `// empty` keeps a host without that field at the empty
# string rather than the literal "null".
#
# Read BEFORE the availability check below, because the notices that check prints are
# held to one firing per session and the session id is what keys them. jq failing here
# leaves the session empty, which guard_notice_once handles as an unidentified session.
event=$(cat)

# A payload that opens like an envelope but does not parse is a TRUNCATED one, not a command
# line. Judged as text it matches no rule and passes, so it is refused here while its shape
# still says what it was. magus denies an unreadable payload for the same reason, and leaves
# this case to its caller because nothing inside it ever saw the bytes.
case $event in
  '{'*)
    if ! printf '%s' "$event" | jq -e . >/dev/null 2>&1; then
      printf '%s' "$__MAGUS_UNREADABLE_RESPONSE"
      exit 0
    fi
    ;;
esac

session=$(printf '%s' "$event" | jq -r ".$HOST_SESSION_PATH // empty" 2>/dev/null)
transcript=$(printf '%s' "$event" | jq -r ".$HOST_TRANSCRIPT_PATH // empty" 2>/dev/null)
event_name=$(printf '%s' "$event" | jq -r '.hook_event_name // empty' 2>/dev/null)

# plain_push succeeds when the call is one bare push through a backend magus drives (`git
# push`, `hg push`, `sl push`, `jj git push`), the only shapes the Codex prompt rules match,
# and sets push_rule to the pattern its rule carries. Anything else (a compound line, `git
# -C dir push`, an MCP call) reaches no rule, so Codex would run it unprompted, and no answer
# here may assume it prompts.
plain_push() {
  [ -z "$HOST_EVENT_RAW" ] || return 1
  push_line=$(printf '%s' "$event" | jq -r ".$HOST_EVENT_PATH // empty" 2>/dev/null)
  case $push_line in
  *[\;\&\|\`\$\(\)\<\>\\]* | *"
"*) return 1 ;;
  "git push" | "git push "*) push_rule='"git", *"push"' ;;
  "hg push" | "hg push "*) push_rule='"hg", *"push"' ;;
  "sl push" | "sl push "*) push_rule='"sl", *"push"' ;;
  "jj git push" | "jj git push "*) push_rule='"jj", *"git", *"push"' ;;
  *) return 1 ;;
  esac
  return 0
}

# codex_cannot_prompt prints why Codex will not put this call in front of the person, and
# nothing when its own approval prompt will. It walks up from the session directory to the
# rules file, the same way Codex finds a project's .codex layer.
codex_cannot_prompt() {
  mode=$(printf '%s' "$event" | jq -r '.permission_mode // empty' 2>/dev/null)
  case $mode in
  bypassPermissions | dontAsk)
    printf 'this Codex session runs in permission_mode %s, which never prompts' "$mode"
    return
    ;;
  esac
  plain_push || {
    printf 'no Codex approval rule matches this call, only a plain git push, hg push, sl push or jj git push command'
    return
  }
  # Anchored on the opening bracket, so the git rule is not found inside jj's ["jj", "git", "push"].
  rules_dir=$PWD
  while [ -n "$rules_dir" ]; do
    if [ -f "$rules_dir/.codex/rules/magus.rules" ]; then
      grep -q "\[ *$push_rule *\]" "$rules_dir/.codex/rules/magus.rules" && return
      break
    fi
    rules_dir=${rules_dir%/*}
  done
  printf 'no .codex/rules/magus.rules carries the prompt rule for this push, which magus agent harness apply --id codex writes'
}

# Codex is recognized by its event as well as by name, so a Codex wiring that forgot
# __MAGUS_AGENT_NAME still never receives permissionDecision "ask", which it would run
# unasked. turn_id is a required field of Codex's published PreToolUse input
# (testdata/hosts/codex/pre-tool-use.command.input.schema.json) and of its
# PermissionRequest input; no vendored Claude Code or Cursor schema names it.
#
# The two are kept apart rather than or-ed, because the arms below trust them differently.
codex_named=
codex_inferred=
[ "$__MAGUS_AGENT_NAME" = codex ] && codex_named=1
[ "$(printf '%s' "$event" | jq -r 'has("turn_id")' 2>/dev/null)" = true ] && codex_inferred=1
codex=
{ [ -n "$codex_named" ] || [ -n "$codex_inferred" ]; } && codex=1

# renders_ask is the --renders-ask claim: this call's reply puts an ask in front of the
# person, or refuses it, and never lets it through unasked. Only a reply this file assembled
# can make that claim. A HOST_RESPONSE the reader wrote gets no flag, so magus answers it
# with a deny rather than a decision it may render as nothing.
renders_ask=

# The ask arm, per host. The default is Claude Code's prompt. Codex gets a pass-through
# with the reason as context when its rules will prompt, and a deny otherwise: see the
# header for why this file never sends Codex permissionDecision "ask".
if [ -z "$HOST_ASK_BRANCH" ]; then
  if [ -n "$codex" ]; then
    # Only a wiring that NAMED itself Codex may render an ask as context the agent is free
    # to skip. Inferring the host from a turn_id key is a guess over an envelope nobody
    # schema-types, and the two ways of being wrong are not equal: the context arm turns an
    # ask into a note that is silently ignored, while the deny arm turns it into a refusal
    # the person can act on. A host that adds turn_id therefore costs a deny, not a pass.
    if [ -n "$codex_named" ]; then
      ask_blocker=$(codex_cannot_prompt)
    else
      ask_blocker='this event looks like Codex but the wiring never said so, and only a config that sets __MAGUS_AGENT_NAME=codex is taken at its word here'
    fi
    if [ -z "$ask_blocker" ]; then
      HOST_ASK_BRANCH='{{else if eq .decision "ask"}}{"hookSpecificOutput":{"hookEventName":"PreToolUse","additionalContext":{{toJson .reason}}}}'
    else
      HOST_ASK_BRANCH='{{else if eq .decision "ask"}}{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":{{toJson (print .reason "\n\nThis call needs the approval of the person you work for, and '"$ask_blocker"'. Ask them to run it from their own terminal.")}}}}'
    fi
  else
    HOST_ASK_BRANCH='{{else if eq .decision "ask"}}{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"ask","permissionDecisionReason":{{toJson .reason}}}}'
  fi
fi
# A decision this file does not know is refused, never allowed: the guard contract grows,
# and a copy older than the growth must not read the new verdict as a pass.
unknown_decision='{{else}}{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":{{toJson (print "magus guard returned the decision " .decision ", which this hook does not know, so it refuses the call rather than allow it. Update the hook template from the magus docs.")}}}}{{end}}'
if [ -z "$HOST_RESPONSE" ] && [ "$event_name" = PermissionRequest ]; then
  # Codex raises PermissionRequest just before its own approval prompt. No decision object
  # leaves the prompt to the person; allow skips it, and is answered only for a plain push,
  # because this event fires for every approval Codex asks and a pass from the guard is not
  # the person's consent to anything else.
  no_decision='{"hookSpecificOutput":{"hookEventName":"PermissionRequest"}}'
  covered=$no_decision
  plain_push && covered='{"hookSpecificOutput":{"hookEventName":"PermissionRequest","decision":{"behavior":"allow"}}}'
  HOST_RESPONSE='{{if eq .decision "deny"}}{"hookSpecificOutput":{"hookEventName":"PermissionRequest","decision":{"behavior":"deny","message":{{toJson .reason}}}}}{{else if eq .decision "ask"}}'"$no_decision"'{{else if eq .decision "pass"}}'"$covered"'{{else if eq .decision "advise"}}'"$covered"'{{else}}{"hookSpecificOutput":{"hookEventName":"PermissionRequest","decision":{"behavior":"deny","message":{{toJson (print "magus guard returned the decision " .decision ", which this hook does not know, so it refuses the call rather than allow it. Update the hook template from the magus docs.")}}}}}{{end}}'
  renders_ask=--renders-ask
fi
if [ -z "$HOST_RESPONSE" ]; then
  HOST_RESPONSE='{{if eq .decision "deny"}}{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":{{toJson .reason}}}}'"$HOST_ASK_BRANCH$HOST_ADVISE_BRANCH"'{{else if eq .decision "advise"}}{{else if eq .decision "pass"}}'"$unknown_decision"
  renders_ask=--renders-ask
fi

# guard_notice_once succeeds the first time $1 fires in this session and fails on every
# repeat, so a caller writes `guard_notice_once <family> && printf ...`.
#
# The notices it holds report a BROKEN INSTALLATION. That is a fact for the person, and
# there is nothing in it an agent can act on, so a repeat is pure noise: measured at 2,741
# firings over recent sessions, 99% of them same-session repeats of text already declined.
#
# The marker lives under TMPDIR rather than in magus's own state because this runs when
# magus is missing or too broken to judge, so it cannot ask magus for anything. cksum and
# mkdir are POSIX; creating the marker is idempotent, so two concurrent tool calls race to
# the same harmless result.
#
# A host that reports no session id shares one marker aged out after __MAGUS_NOTICE_WINDOW
# minutes, so the first session on such a host cannot silence every session after it.
guard_notice_once() {
  notice_dir=${TMPDIR:-/tmp}/magus-guard-notices
  notice_key=$(printf '%s' "${session:-anon}" | cksum | cut -d' ' -f1)
  notice_marker=$notice_dir/$notice_key.$1
  mkdir -p "$notice_dir" 2>/dev/null || return 0
  if [ -f "$notice_marker" ]; then
    [ -n "$session" ] && return 1
    find "$notice_marker" -mmin +"${__MAGUS_NOTICE_WINDOW:-120}" 2>/dev/null | grep -q . || return 1
  fi
  : > "$notice_marker" 2>/dev/null
  return 0
}

if [ -z "$__MAGUS_BIN" ] || [ ! -x "$__MAGUS_BIN" ]; then
  guard_notice_once unavailable && printf '%s' "$__MAGUS_UNAVAILABLE_RESPONSE"
  exit 0
fi

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
#
# __MAGUS_SHELL_FLAGS rides the same retry. It is how a wiring DECLARES a capability it
# provides - today `--observes-skill-loads`, set by a config that also matches the host's
# skill tool - and a rule that needs one stands down when it is absent. That makes the
# fallback below the correct degradation rather than a loss: a binary too old for the flag
# drops it, magus hears no claim, and the rule it would have armed goes quiet instead of
# denying something the reader cannot fix. Word-split on purpose, so a wiring may pass more
# than one; keep the values flag-shaped and space-separated.
# shellcheck disable=SC2086
guard() {
  if [ -n "$HOST_EVENT_RAW" ]; then
    printf '%s' "$event" | "$__MAGUS_BIN" shell $__MAGUS_SHELL_FLAGS "$@" -o "template=$HOST_RESPONSE"
  else
    printf '%s' "$event" | jq -r ".$HOST_EVENT_PATH" | "$__MAGUS_BIN" shell $__MAGUS_SHELL_FLAGS "$@" -o "template=$HOST_RESPONSE"
  fi
}

# guard_failure_notice states WHICH binary went silent, what version it is, and what it
# actually said - the three facts a reader otherwise spends a session collecting.
#
# The text this replaces offered two suspects, "too old for session hook, or cannot load
# this workspace", and the second is not a cause at all: the deny rules need no workspace,
# and a current binary run from an empty directory still denies. Naming a suspect the
# evidence does not support is worse than naming none, because the reader goes and checks it.
#
# It re-runs the guard to capture stderr, which the verdict path discards. One extra
# process, only on the path that is already broken - the same trade the attribution retry
# above makes. WARN lines are dropped because a config the binary is too old to parse warns
# BEFORE it fails, and that warning is a symptom of the same staleness, not the error.
guard_failure_notice() {
  ver=$("$__MAGUS_BIN" version 2>/dev/null | head -n 1)
  [ -n "$ver" ] || ver='version unreadable'
  why=$(guard 2>&1 >/dev/null | grep -v 'WARN' | head -n 1)
  [ -n "$why" ] || why='it printed no error'
  printf 'magus guard is NOT running: %s (%s) could not judge this command, so its deny and advise rules are unenforced. It said: %s. Rebuild or update THAT binary to restore the guard.' \
    "$__MAGUS_BIN" "$ver" "$why"
}
# A DENY exits non-zero (2) with the verdict on stdout, so a bare `||` retry would treat
# every blocked command as "this binary rejected the attribution flags" and judge it a
# second time - unattributed, and recorded twice in the activity trail. Emptiness alone
# cannot tell the cases apart either, because a pass renders empty on purpose. Both
# together can: a rejected flag prints its usage to STDERR and leaves stdout empty, while
# any real verdict that is not a pass leaves something on stdout.
#
# --renders-ask rides the attributed call only. A binary too old for it is too old to ask,
# so the retry dropping it loses nothing.
# shellcheck disable=SC2086
verdict=$(guard --agent-name "$__MAGUS_AGENT_NAME" --session "$session" --transcript "$transcript" $renders_ask 2>/dev/null)
status=$?
if [ "$status" -ne 0 ] && [ -z "$verdict" ]; then
  verdict=$(guard 2>/dev/null)
  status=$?
fi

# A PASS and a BROKEN GUARD both render nothing, and telling them apart is the whole
# point of this block. A pass exits 0 with empty output because there was nothing to
# say; a binary that cannot run - too old for `session hook`, unable to load the workspace,
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
#
# Said once per session. The line above used to repeat on every tool call, on the
# reasoning that nothing survives between two of them; guard_notice_once is what does,
# without asking the binary that just failed to run for anything. Twenty copies of
# identical text is wallpaper, and wallpaper is how a real failure goes unread.
if [ "$status" -ne 0 ] && [ -z "$verdict" ]; then
  if guard_notice_once failed; then
    if [ -n "$__MAGUS_FAILED_RESPONSE" ]; then
      printf '%s' "$__MAGUS_FAILED_RESPONSE"
    else
      guard_failure_notice | jq -Rc '{hookSpecificOutput:{hookEventName:"PreToolUse",additionalContext:.}}'
    fi
  fi
  exit 0
fi
printf '%s' "$verdict"
```

## `magus-path.sh`

The declared-output guard. Wire it to your host's file-editing tool rather than
its shell tool. It explains rather than blocks: editing a generated file is
wasteful, not destructive.

```sh
#!/usr/bin/env sh
# magus guard hook: judges ONE file path an agent is about to write.
#
# Companion to magus-command.sh, wired to your host's file-editing tool
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
# __MAGUS_AGENT_NAME and HOST_SESSION_PATH work exactly as they do in
# magus-command.sh: attribution recorded on the activity event, never an
# input to the verdict.
#
# Coverage declaration, machine-read by the host-parity gate - see the longer
# note in magus-command.sh. It records what HOST_RESPONSE RENDERS, not
# which rules currently fire, so deny=model is true the moment the arm exists.
#
# No rule asks on this surface today. The arm exists for the same reason the deny arm did
# before its first rule: an installed copy never self-corrects. Claude Code prompts on it;
# Codex does not support a hook ask and no Codex rule prompts for a write, so there it
# renders as a deny.
# magus-guard-template: 16
# magus-guard-coverage: schema=1 host=claude-code surface=path deny=model advise=model pass=none ask=human
# magus-guard-coverage: schema=1 host=codex surface=path deny=model advise=model pass=none ask=model

# Plain assignment, NOT ${VAR:=default}: the response template is full of `}`
# and the first one would terminate a ${...} expansion.
[ -n "$HOST_EVENT_PATH" ] || HOST_EVENT_PATH='tool_input.file_path'
[ -n "$HOST_SESSION_PATH" ] || HOST_SESSION_PATH='session_id'
[ -n "$HOST_TRANSCRIPT_PATH" ] || HOST_TRANSCRIPT_PATH='transcript_path'
[ -n "$__MAGUS_AGENT_NAME" ] || __MAGUS_AGENT_NAME='claude-code'
# The one arm on this surface that does not fail open; see the truncated-envelope check
# below. Plain assignment for the same reason as the rest: a `}` would end a ${...}.
[ -n "$__MAGUS_UNREADABLE_RESPONSE" ] || __MAGUS_UNREADABLE_RESPONSE='{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":"magus guard could not read this write from the host, so nothing was judged. A payload that arrives truncated reads exactly like an empty one, which is why this is blocked rather than cleared. Retry the call."}}'
# Same split, and the same reason, as in magus-command.sh: a host that
# REJECTS the context key can mark the hook run failed and continue the call, so an
# advisory it cannot take disarms that call rather than merely going unread. No
# host wired to this file is in that position; the flag is there for the one you
# may wire.
if [ -n "$__MAGUS_NO_ADVISE" ]; then
  HOST_ADVISE_BRANCH=''
else
  [ -n "$HOST_ADVISE_BRANCH" ] || HOST_ADVISE_BRANCH='{{else if eq .decision "advise"}}{"hookSpecificOutput":{"hookEventName":"PreToolUse","additionalContext":{{toJson .context}}}}'
fi
# HOST_RESPONSE's default is assembled once the event is read, because telling Codex apart
# reads the event; see magus-command.sh.
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
while [ -n "$guard_root" ] && [ -z "$__MAGUS_BIN" ]; do
  if [ -f "$guard_root/magusfile.buzz" ]; then
    [ -x "$guard_root/magus" ] && __MAGUS_BIN=$guard_root/magus
    break
  fi
  guard_root=${guard_root%/*}
done
[ -n "$__MAGUS_BIN" ] || __MAGUS_BIN=$(command -v magus 2>/dev/null)

if [ -z "$__MAGUS_BIN" ] || [ ! -x "$__MAGUS_BIN" ]; then
  # Prints nothing by default: for most hosts an empty response means "allow".
  # Set __MAGUS_UNAVAILABLE_RESPONSE for a host that needs an explicit verdict.
  [ -n "$__MAGUS_UNAVAILABLE_RESPONSE" ] && printf '%s' "$__MAGUS_UNAVAILABLE_RESPONSE"
  exit 0
fi

# One drain of stdin, two selections from it: the path to judge, and the session
# id to attribute it to. `// empty` keeps a host without that field at the empty
# string rather than the literal "null".
event=$(cat)

# A payload that opens like an envelope but does not parse is a TRUNCATED one, not a path.
# Judged as text it matches no rule and passes, so it is refused here while its shape still
# says what it was. magus itself denies an unreadable payload for the same reason.
case $event in
  '{'*)
    if ! printf '%s' "$event" | jq -e . >/dev/null 2>&1; then
      printf '%s' "$__MAGUS_UNREADABLE_RESPONSE"
      exit 0
    fi
    ;;
esac

session=$(printf '%s' "$event" | jq -r ".$HOST_SESSION_PATH // empty")
transcript=$(printf '%s' "$event" | jq -r ".$HOST_TRANSCRIPT_PATH // empty")

# Which payload magus gets: the WHOLE envelope, or the one string HOST_EVENT_PATH selects.
# magus reads a write target off the ENVELOPE under every `*_path` spelling, so selecting one
# dot-path here left NotebookEdit's `notebook_path` unjudged. See magus-path.buzz.
whole_event=1
if [ "$(printf '%s' "$event" | jq -r '(.tool_name // "") | startswith("mcp__")' 2>/dev/null)" != true ] &&
  [ "$(printf '%s' "$event" | jq -r ".$HOST_EVENT_PATH | type" 2>/dev/null)" = string ]; then
  whole_event=
fi

# Codex by name or by its event's turn_id, exactly as magus-command.sh decides it. No
# Codex rule prompts for a write, so there an ask renders as a deny.
if [ -z "$HOST_ASK_BRANCH" ]; then
  if [ "$__MAGUS_AGENT_NAME" = codex ] || [ "$(printf '%s' "$event" | jq -r 'has("turn_id")' 2>/dev/null)" = true ]; then
    HOST_ASK_BRANCH='{{else if eq .decision "ask"}}{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":{{toJson (print .reason "\n\nThis write needs the approval of the person you work for, and Codex has no prompt for it. Ask them to make it themselves.")}}}}'
  else
    HOST_ASK_BRANCH='{{else if eq .decision "ask"}}{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"ask","permissionDecisionReason":{{toJson .reason}}}}'
  fi
fi
# A decision this file does not know is refused, never allowed; see magus-command.sh.
# Only a reply assembled here claims --renders-ask: a HOST_RESPONSE the reader wrote gets a
# deny from magus rather than an ask it may render as nothing.
renders_ask=
if [ -z "$HOST_RESPONSE" ]; then
  HOST_RESPONSE='{{if eq .decision "deny"}}{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":{{toJson .reason}}}}'"$HOST_ASK_BRANCH$HOST_ADVISE_BRANCH"'{{else if eq .decision "advise"}}{{else if eq .decision "pass"}}{{else}}{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":{{toJson (print "magus guard returned the decision " .decision ", which this hook does not know, so it refuses the call rather than allow it. Update the hook template from the magus docs.")}}}}{{end}}'
  renders_ask=--renders-ask
fi

# Attribution is BEST EFFORT; the verdict is not. --agent-name and --session postdate the current magus
# release, and an older binary rejects the unknown flag outright - printing usage to stdout and
# exiting non-zero - which leaves the host with no verdict rather than an unattributed one. Try with
# attribution, fall back to the call this script made before it existed.
guard() {
  if [ -n "$whole_event" ]; then
    printf '%s' "$event"
  else
    printf '%s' "$event" | jq -r ".$HOST_EVENT_PATH"
  fi | "$__MAGUS_BIN" shell --path "$@" -o "template=$HOST_RESPONSE"
}
# Same discrimination as magus-command.sh, and for the same reason now that this
# surface can render a deny: a DENY exits non-zero (2) with the verdict on stdout, so a
# bare `||` retry would treat every blocked write as "this binary rejected the attribution
# flags" and judge it a second time - unattributed, and recorded twice in the activity
# trail. Emptiness alone cannot tell the cases apart either, because a pass renders empty
# on purpose. Both together can: a rejected flag prints its usage to STDERR and leaves
# stdout empty, while any real verdict that is not a pass leaves something on stdout.
# shellcheck disable=SC2086
verdict=$(guard --agent-name "$__MAGUS_AGENT_NAME" --session "$session" --transcript "$transcript" $renders_ask 2>/dev/null)
status=$?
if [ "$status" -ne 0 ] && [ -z "$verdict" ]; then
  verdict=$(guard 2>/dev/null)
  status=$?
fi

# A pass and a broken guard both render nothing; see magus-command.sh for why
# telling them apart matters. Kept identical here so neither surface grows a behavior
# the other lacks - the difference is only that this one has no default message,
# because for most hosts an empty response on this surface already means "allow".
if [ "$status" -ne 0 ] && [ -z "$verdict" ]; then
  [ -n "$__MAGUS_FAILED_RESPONSE" ] && printf '%s' "$__MAGUS_FAILED_RESPONSE"
  exit 0
fi
printf '%s' "$verdict"
```

## `magus-observe.sh`

The one template that carries no verdict. Wire it to the tools that only LOOK -
your host's read equivalent - and it records the path the agent reached without
judging it. It prints nothing and always exits 0.

Do not point a read tool at `magus-path.sh` instead. A read event carries
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
# and pipes it into `magus session hook --observe`. It prints NOTHING and always exits
# 0 - see the note on that below, which is load-bearing rather than tidy.
# Override any of the variables below:
#
#   HOST_EVENT_PATH  dot-path to the read path inside your host's event
#   HOST_SESSION_PATH  dot-path to the session id inside your host's event
#   HOST_TRANSCRIPT_PATH  dot-path to your host's own log of this session
#   __MAGUS_AGENT_NAME  the agent host name recorded alongside the observation
#   __MAGUS_BIN  path to the binary, when it is not on PATH
#
# The defaults are Claude Code's event shape, matching its two siblings. A
# different host overrides the dot-paths and passes its own __MAGUS_AGENT_NAME,
# exactly as codex-hooks.json already does for the guard templates.
#
# NO magus-guard-coverage line, and that absence is deliberate rather than an
# oversight: a coverage declaration states how much of a VERDICT a host can
# carry on a guard surface, and this file carries no verdict on no surface. It
# never denies, never advises, and cannot change what your host does next. The
# parity gates ask that question only of artifacts that answer it.
#
# magus-guard-template: 16

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
[ -n "$__MAGUS_AGENT_NAME" ] || __MAGUS_AGENT_NAME='claude-code'
# Prefer the workspace's own ./magus over PATH, for the same reason its two siblings do - and
# this file needs it MORE than they do, because it is silent by design. An older PATH copy
# does not know --observe at all: it rejects the flag, prints its usage to a stream this
# script discards, and exits non-zero into an `|| true` - so the observation is simply never
# recorded, forever, with nothing anywhere saying so. Measured 2026-08-14 in magus's own
# repository, where the wiring was correct, the binary was wrong, and the trail held 3252
# events and not one read.
#
# Found by walking UP to the magusfile, not by testing ./magus alone: a hook runs in the
# host's session directory, which is not always the workspace root. The command template
# carries the full reasoning.
guard_root=$PWD
while [ -n "$guard_root" ] && [ -z "$__MAGUS_BIN" ]; do
  if [ -f "$guard_root/magusfile.buzz" ]; then
    [ -x "$guard_root/magus" ] && __MAGUS_BIN=$guard_root/magus
    break
  fi
  guard_root=${guard_root%/*}
done
[ -n "$__MAGUS_BIN" ] || __MAGUS_BIN=$(command -v magus 2>/dev/null)

# An absent observer is SILENT, where an absent guard is loud.
#
# The guard templates announce themselves when magus cannot be found, because an
# unenforced deny rule is a safety fact the reader needs. Nothing is unenforced
# here - there is no rule - so the same announcement would be a per-read
# interruption reporting that an optional record was not written.
if [ -z "$__MAGUS_BIN" ] || [ ! -x "$__MAGUS_BIN" ]; then
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
# missing from `magus session`, the binary is too old. Both streams are
# discarded because a flag-parse error would otherwise reach the host as this
# hook's response on every read.
printf '%s' "$path" | "$__MAGUS_BIN" shell --observe \
  --agent-name "$__MAGUS_AGENT_NAME" --session "$session" --transcript "$transcript" \
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
shipped `magus-path.sh` had the opposite gap and dropped denials.

**A pass and a broken guard both render nothing.** A pass exits 0 with empty
output because there was nothing to say. A binary that cannot run - too old for
`session hook`, unable to load the workspace, half-written by a concurrent build - exits
non-zero with empty output. Printing that as a pass disables every rule with
nothing anywhere saying so. Both scripts discriminate on status and emptiness
together, and announce the second case. The announcement names its evidence -
the binary path they resolved, that binary's version, and the first line it
printed on stderr - because the guesses it used to offer sent readers to check a
workspace that was never the problem. Set `__MAGUS_FAILED_RESPONSE` to replace it
with a fixed response of your own.

**Attribution must never break a verdict.** `--agent-name` and `--session`
postdate the current release, and an older binary rejects an unknown flag by
printing usage and exiting non-zero, which leaves the host with no verdict at
all. Both scripts try with attribution and retry without it, and they retry only
when the call produced no verdict - never merely because it exited non-zero,
since a deny exits 2 with the verdict on stdout.

## The Buzz ports

Beside every template on this page sits a `.buzz` file of the same name: the three
guard templates above, and the two verdict-free wrappers documented below. Each
reads the same host event, renders the same reply, and carries the same version
marker; `cmd/magus/testdata/script/guard_templates.txtar` runs both forms against
every recorded event and fails on one byte of difference.

Which one to wire is a question about your machine, not about the guard. The sh
copy needs a POSIX shell and `jq`; the Buzz port needs neither, so it runs
unchanged on Windows and installs nothing. What it does need is a `magus` new
enough to run it, because moving the glue into Buzz moves the interpreter from a
program that is always present to one that has a version. A magus too old to run
the script renders no verdict, and your host reports that as a hook error rather
than passing the call silently; see [Claude Code](claude-code.md) for what that
looks like.

`magus agent harness apply --id claude-code` wires these. The command is
`magus buzz -s <file>`, or `./magus buzz -s <file>` in a workspace that builds its
own binary: `-s` keeps the interpreter's own advisories off stderr, which a host
would otherwise show as a hook error. An entry that declares a capability adds a
`-- <flags>` tail and nothing else; see [why the two forms differ on knobs](#why-the-two-forms-differ-on-knobs) for what does not appear there.

They import no magus Buzz module and no spell, and a test refuses one. A hook
fires on every tool call, and either import would make it pay for opening the
workspace: about 700ms here, against roughly 10ms for a script that reads none of
it.

### `magus-command.buzz`

The command guard, in Buzz. Same host overrides, same replies, same version marker; it selects the event's fields with `encoding/json` instead of `jq` and reaches the binary with `proc\exec` instead of a pipeline. The two PER-ENTRY knobs are not overrides here: `wholeEvent` reads the raw-event question off the event, and `shellFlags` takes the `magus shell` flags from the script's own argv.

```buzz
// magus guard hook: judges ONE shell command an agent is about to run.
//
// This file is the source of truth for hosts wired to `magus buzz`. It is the
// Buzz port of magus-command.sh and renders byte-identical replies; the
// shell copy stays for hosts wired to `sh`. Buzz needs no jq and no POSIX shell,
// so the two runtime dependencies the shell copy carries are gone and the file
// runs unchanged on Windows.
//
// Run it as `magus buzz -s magus-command.buzz`. `-s` is load-bearing, not tidiness:
// without it a BZZ advisory on stderr reads to the host as a hook error, so the glue
// reports itself broken. It imports no magus Buzz
// module and no spell, which is what keeps the workspace CLOSED: a script that
// reads no workspace member starts in about 10ms, where opening one costs
// roughly 700ms on every tool call.
//
// Contract: reads the host's event as JSON on stdin, selects its command, then
// feeds the command to `magus shell`. It writes the host's response on stdout
// and exits 0 either way.
//
// A hook command is an ARGV, not a shell line: the host splits it and runs the
// program itself, so a `NAME=value` prefix only means anything where something
// re-joins and re-parses the string. The sh copy beside this file is run BY sh and
// takes every knob below from the environment; this one is wired as a plain
// `<interpreter> buzz -s <this file> [-- flags]` and takes the two knobs that vary
// PER ENTRY from what it already has: the event decides whether to forward the whole
// envelope, and the argv after `--` carries the `magus shell` flags this wiring
// declares about itself. Everything else is still an environment variable, because
// it is a property of the HOST rather than of one entry, and a host sets it once.
//
//   -- <flags>       `magus shell` flags this entry declares, one argv word each, parsed
//                    against SUPPORTED_FLAGS below. Capabilities, not policy: a config
//                    that also matches its host's skill tool passes
//                    `-- --observes-skill-loads`, and rules that require a skill load
//                    stand down where it is absent. An argument this file does not know
//                    is REPORTED on stderr and left out, and the call is judged anyway:
//                    a misconfigured entry must not block work, and must not be silent
//
// Override any of the variables below:
//
//   HOST_EVENT_PATH  dot-path to the command inside your host's event
//   HOST_SESSION_PATH  dot-path to the session id inside your host's event
//   HOST_TRANSCRIPT_PATH  dot-path to your host's own log of this session
//   HOST_RESPONSE    Go template rendering your host's reply
//   HOST_ADVISE_BRANCH  the advise arm of that template
//   HOST_ASK_BRANCH  the ask arm of that template: the reply that puts the call in
//                    front of the PERSON through the host's own approval prompt
//   __MAGUS_NO_ADVISE  set it when the host has no context-injection channel, so
//                    an advise renders nothing rather than a reply it rejects
//   __MAGUS_AGENT_NAME  the agent host name recorded alongside the observation
//   __MAGUS_BIN  path to the binary, when it is not on PATH
//   __MAGUS_UNAVAILABLE_RESPONSE  what to print when magus cannot be found, so a
//                    host can choose its own fail-open or fail-closed stance
//   __MAGUS_FAILED_RESPONSE  the same, for a magus that IS found but cannot judge
//                    the command. Left unset, this file builds one from evidence:
//                    which binary it resolved, that binary's version, and the
//                    error it actually printed
//   __MAGUS_UNREADABLE_RESPONSE  what to print when the call never arrived, which is
//                    the one case here that denies rather than failing open
//   __MAGUS_NOTICE_WINDOW  minutes a notice marker survives on a host that reports
//                    no session id, so one session cannot silence every later one
//
// The defaults are Claude Code's event and response shape.
//
// __MAGUS_AGENT_NAME and the session are ATTRIBUTION, not policy. magus records them on
// its activity event so a reader can tell which host produced an observation;
// neither one can change the verdict, and a host whose event carries no session
// id records none and is judged exactly the same.
//
// __MAGUS_BIN is deliberately NOT called MAGUS_BIN: the whole MAGUS_* space is
// magus's own configuration surface, so a variable this template invents must stay
// out of it rather than look like a setting magus reads.
//
// On a missing magus this prints a visible notice rather than exiting quietly. A
// guard that exits silently never runs and nothing says so, and an unguarded
// session you know about beats one you do not.
//
// NOTHING here may raise. Claude Code reads a non-zero hook exit other than 2 as a
// non-blocking error and runs the call anyway, and exit 2 BLOCKS with stderr as the
// message, which would turn a Buzz bug into a refusal the person cannot read. Every
// call that can fail is caught, and the script ends by writing one reply.
//
// The line below declares, per guard surface, how much of a verdict this file
// can carry: model (reaches the agent), human (reaches the person only), or none
// (not delivered). It is machine-read by the host-parity gate, which fails the
// build when a decision or surface exists in the guard contract that some host
// was never asked about. Keep it true to what HOST_RESPONSE actually renders.
//
// An ask reaches the person on both hosts, by different routes. Claude Code takes
// permissionDecision "ask" from PreToolUse and prompts. Codex parses that value and does
// not support it: the hook run is marked failed and the call CONTINUES, so on Codex this
// file never emits it. There the prompt comes from a rules file the codex harness writes
// (.codex/rules/magus.rules, a prefix_rule on git push with decision "prompt"), and the
// PermissionRequest event Codex raises before that prompt reaches this same file, which
// answers allow for a push the gate covers, leaves an ungated one to the person, and
// denies a leased worker's. Where Codex cannot prompt at all (no rules file, a
// permission_mode that never asks, a call no rule matches) the ask renders as a deny that
// names the person's own terminal.
// It declares claude-code only, and no codex row, even though the Codex arms above are
// implemented and graded: a coverage declaration states what a HOST gets, and codex is
// wired to magus-command.sh. The arms ship ahead of the wiring for the same reason
// the deny arm shipped ahead of its first rule, so a Codex config can move to Buzz
// without a window where a decision renders as nothing.
// magus-guard-template: 16
// magus-guard-coverage: schema=1 host=claude-code surface=command deny=model advise=model pass=none ask=human
// magus-guard-coverage: schema=1 host=claude-code surface=mcp deny=model advise=model pass=none ask=human
// claude-code's mcp row is real: an mcp__magus__* PreToolUse call carries no tool_input.command,
// so this file forwards the whole event instead (see wholeEvent), and the same hookSpecificOutput
// reply it already renders for the command surface carries a deny or an advise on this one too.

import "std";
import "io";
import "encoding/json" as json;
import "env";
import "fs";
import "os";
import "path";
import "proc";

// ADVISE_DEFAULT and the two ask arms are separate strings because a host may
// replace one arm without replacing the whole reply. A host that REJECTS the
// context key is worse off than one that ignores it: an unsupported field can
// make the host mark the hook run failed and continue the call, so an advisory
// it cannot take disarms the guard rather than merely going unread. No host
// shipped here is in that position today, since both hosts wired to this file
// take additionalContext, so __MAGUS_NO_ADVISE has no user and is kept for the
// one you may wire.
final ADVISE_DEFAULT = `{{else if eq .decision "advise"}}{"hookSpecificOutput":{"hookEventName":"PreToolUse","additionalContext":{{toJson .context}}}}`;

final ASK_CLAUDE = `{{else if eq .decision "ask"}}{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"ask","permissionDecisionReason":{{toJson .reason}}}}`;

final ASK_CODEX_PROMPTS = `{{else if eq .decision "ask"}}{"hookSpecificOutput":{"hookEventName":"PreToolUse","additionalContext":{{toJson .reason}}}}`;

// A backtick string DOES interpolate: a `{...}` run is evaluated when its contents parse as
// a Buzz expression, and stays literal only when they do not. Go template braces do not
// parse, which is the only reason these render verbatim, and a `{name}` fragment would not.
// So a template is spliced through this placeholder rather than concatenated around an
// open `{{`, which would swallow its own closing backtick.
final BLOCKER_SLOT = "__MAGUS_ASK_BLOCKER__";
final ASK_CODEX_BLOCKED = `{{else if eq .decision "ask"}}{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":{{toJson (print .reason "\n\nThis call needs the approval of the person you work for, and __MAGUS_ASK_BLOCKER__. Ask them to run it from their own terminal.")}}}}`;

// A decision this file does not know is refused, never allowed: the guard contract
// grows, and a copy older than the growth must not read the new verdict as a pass.
final UNKNOWN_DECISION = `{{else}}{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":{{toJson (print "magus guard returned the decision " .decision ", which this hook does not know, so it refuses the call rather than allow it. Update the hook template from the magus docs.")}}}}{{end}}`;

final DENY_HEAD = `{{if eq .decision "deny"}}{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":{{toJson .reason}}}}`;
final PASS_AND_ADVISE_TAIL = `{{else if eq .decision "advise"}}{{else if eq .decision "pass"}}`;

final PERMISSION_NO_DECISION = `{"hookSpecificOutput":{"hookEventName":"PermissionRequest"}}`;
final PERMISSION_ALLOW = `{"hookSpecificOutput":{"hookEventName":"PermissionRequest","decision":{"behavior":"allow"}}}`;
final PERMISSION_DENY_HEAD = `{{if eq .decision "deny"}}{"hookSpecificOutput":{"hookEventName":"PermissionRequest","decision":{"behavior":"deny","message":{{toJson .reason}}}}}{{else if eq .decision "ask"}}`;
final PERMISSION_UNKNOWN = `{{else}}{"hookSpecificOutput":{"hookEventName":"PermissionRequest","decision":{"behavior":"deny","message":{{toJson (print "magus guard returned the decision " .decision ", which this hook does not know, so it refuses the call rather than allow it. Update the hook template from the magus docs.")}}}}}{{end}}`;

final CONTEXT_SLOT = "__MAGUS_CONTEXT__";
final CONTEXT_REPLY = `{"hookSpecificOutput":{"hookEventName":"PreToolUse","additionalContext":__MAGUS_CONTEXT__}}`;

// The prose, not the reply: the two surfaces wrap it differently, and holding it once is
// what keeps them from drifting into two different sentences about one fact.
final UNAVAILABLE_TEXT = "magus guard is NOT running: magus is not on PATH, so its deny and advise rules are unenforced right now. Install magus, or set __MAGUS_BIN to its path, to restore the guard.";

// The one arm here that does not fail open. Elsewhere magus is missing or cannot answer;
// here nothing arrived, so no rule was ever offered the call. `magus shell` denies an
// unreadable payload for the same reason and leaves this case to its caller.
final UNREADABLE_DEFAULT = `{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":"magus guard could not read this call from the host, so nothing was judged. A payload that arrives truncated reads exactly like an empty one, which is why this is blocked rather than cleared. Retry the call."}}`;

// The characters that let one command line become several, or become a different
// one. A push carrying any of them reaches no Codex prefix rule.
final SHELL_METACHARACTERS = [";", "&", "|", "`", "$", "(", ")", "<", ">", "\\", "\n"];

// The namespace a host gives a tool served over MCP. Not a host's own tool vocabulary,
// which stays in its matcher: this is the wire convention `magus shell` itself reads a
// tool call out of, and wholeEvent explains why the name has to be tested at all.
final MCP_TOOL_PREFIX = "mcp__";

// What magus's own decoder tests to tell an envelope from a bare command line. `\{` escapes
// the interpolation a lone brace would open.
final ENVELOPE_PREFIX = "\{";

// Every `magus shell` flag an entry may declare on this script's argv, by exact spelling.
//
// A list rather than a rule, because this file has to be able to say "I do not know that
// one". Capabilities only: a flag here states something about the WIRING that the event
// cannot state for itself, which today is one thing, whether the config also matches its
// host's skill tool. Policy stays in the workspace's own rules, where a reader can see it
// and magus can change it without every installed config being edited.
final SUPPORTED_FLAGS = ["--observes-skill-loads"];

fun envOr(name: str, fallback: str) > str {
    final value = env\get(name) catch "";
    if (value == "") { return fallback; }
    return value;
}

fun digKey(node: any?, key: str) > any? {
    final m = node as? {str: any};
    if (m == null) { return null; }
    return m![key];
}

// dig walks a jq-style dot-path and returns null where jq would print nothing.
fun dig(event: any?, dotPath: str) > any? {
    var cur = event;
    foreach (key in dotPath.split(".")) {
        cur = digKey(cur, key: key);
        if (cur == null) { return null; }
    }
    return cur;
}

// render prints a value the way `jq -r` does: a string bare, anything else as JSON.
fun render(value: any) > str {
    if (value is str) { return value as str; }
    return json\stringify(value) catch "null";
}

// field is `jq -r ".<dotPath> // empty"`: absent reads as the empty string rather
// than the literal "null".
fun field(event: any?, dotPath: str) > str {
    final value = dig(event, dotPath: dotPath);
    if (value == null) { return ""; }
    return render(value);
}

// rawField is `jq -r ".<dotPath>"` with no default, so an absent field reaches
// magus as the literal "null".
fun rawField(event: any?, dotPath: str) > str {
    final value = dig(event, dotPath: dotPath);
    if (value == null) { return "null"; }
    return render(value);
}

// wholeEvent decides which of the two payloads `magus shell` gets: the WHOLE envelope,
// or the one string HOST_EVENT_PATH selects out of it.
//
// The event answers this itself, which is why no entry has to. Selecting a field is the
// NARROW case and needs two facts to be true at once; everything else forwards the
// envelope, because magus's own decoder knows every payload shape magus reads (a command,
// a written path, a skill name, a spawn prompt, an MCP call) and answers "nothing to
// judge" for the rest, while a field selected out of a shape this file did not recognize
// hands the guard a string that is not a command and gets it judged as one. Judging the
// wrong string is the failure worth engineering against: it is silent in both directions,
// passing a call nobody looked at or denying one for words that were never a command.
// Refusing instead is not the safer answer here, because this file fails OPEN by doctrine
// (see the notice arms below): a deny it cannot justify is one a reader routes around.
//
// The two facts:
//
//   1. The tool is not an MCP call. `magus shell` reads an MCP call off the TOOL NAME and
//      renders it itself, ahead of any `command` in the params, so a params object that
//      happens to carry a `command` key must not be reduced to that key. `mcp__<server>__
//      <tool>` is the naming convention the hosts wired to this file use, and matching the
//      namespace only ever ADDS a whole-envelope answer: a host that spells MCP some other
//      way still lands there whenever its payload carries no command string, which is the
//      ordinary case.
//   2. HOST_EVENT_PATH selects a STRING. A spawn is a prompt plus a subagent type, a skill
//      load is a skill name, an MCP call is a tool name plus params: none of them carries
//      `tool_input.command`, so the field is absent and the envelope goes whole. Present
//      but not a string (null, an object, a number) is a shape this file cannot read, and
//      takes the same road for the reason above.
//
// No host publishes a schema that could settle this on types instead. The one vendored
// schema that names the field at all is Codex's published pre-tool-use input
// (testdata/hosts/codex/pre-tool-use.command.input.schema.json), which requires
// `tool_input` and declares it `true`: present, unconstrained. Claude Code publishes a
// schema for its SETTINGS and one for hook STDOUT, neither of which types an event, and
// Cursor publishes no hook input schema at all. Shape is the only fact on offer, which is
// why the safe direction has to be the one that reads the whole thing.
fun wholeEvent(event: any?, dotPath: str, toolPath: str) > bool {
    if (field(event, dotPath: toolPath).startsWith(MCP_TOOL_PREFIX)) { return true; }
    final value = dig(event, dotPath: dotPath);
    if (value == null) { return true; }
    return !(value is str);
}

fun hasKey(event: any?, key: str) > bool {
    final m = event as? {str: any};
    if (m == null) { return false; }
    return m![key] != null;
}

// trimTrailingNewlines matches shell command substitution, which drops every
// trailing newline from a captured verdict.
fun trimTrailingNewlines(s: str) > str {
    var end = s.len();
    while (end > 0 and s.sub(end - 1, len: 1) == "\n") { end = end - 1; }
    return s.sub(0, len: end);
}

fun firstLine(s: str) > str {
    return s.split("\n")[0];
}

// isExecutable answers "could proc\exec run this", which a directory cannot: one named
// `magus` at the workspace root carries the execute bits too, and accepting it sent the
// caller to the arm that names a path it then cannot run.
fun isExecutable(candidate: str) > bool {
    final info = fs\stat(candidate) catch null;
    if (info == null) { return false; }
    if ((info!.is_dir as? bool) ?? false) { return false; }
    final mode = info!.mode as? int;
    if (mode == null) { return false; }
    // 73 is 0111, the three execute bits. Parenthesized for the reader, not the parser:
    // Buzz binds `&` tighter than `!=`, which is the opposite of C and Go.
    return (mode! & 73) != 0;
}

// resolveBin prefers the workspace's own ./magus over PATH. A repository that builds
// magus, or pins a newer one than is installed, keeps its RULES in that binary, and an
// older PATH copy does not fail loudly when it lacks them. It does not recognize the
// config key that ARMS a rule, warns about an unknown field, and returns pass: silent
// non-enforcement at exit 0. Measured 2026-08-13, when a write into a declared notes
// store was allowed by a binary that predated the knowledge.notes key while `magus
// doctor` reported the guard as fine.
//
// Found by walking UP to the magusfile, not by testing ./magus alone. A hook runs in the
// host's session directory, and that is not always the workspace root: a session opened
// in a subdirectory, or opened in one checkout while the work happens in another, tests a
// ./magus that is not there and falls through to PATH. Where PATH's copy cannot load the
// workspace at all, that is the entire guard failing open, measured 2026-08-27, when a
// piped `magus affected ci` that the rules DO deny ran unjudged.
fun resolveBin() > str {
    final declared = env\get("__MAGUS_BIN") catch "";
    if (declared != "") { return declared; }
    var dir = path\abs(".") catch "";
    while (dir != "") {
        final marker = "{dir}/magusfile.buzz";
        final found = fs\isFile(marker) catch false;
        if (found) {
            final candidate = "{dir}/magus";
            if (isExecutable(candidate)) { return candidate; }
            break;
        }
        dir = parentDir(dir);
    }
    return proc\which("magus") catch "";
}

// parentDir is the shell's ${dir%/*}: it strips the last path element and yields
// the empty string at the top, which is what ends the walk. fs\dirname is not that
// function, because it answers "/" for a top-level directory and the walk would
// never terminate.
//
// Both separators, because this file claims to run unchanged on Windows. Splitting on `/`
// alone made `C:\...` a single part, so the walk never ran once and resolveBin fell
// straight through to PATH, which is the staleness it exists to avoid.
fun parentDir(dir: str) > str {
    var i = dir.len() - 1;
    while (i >= 0) {
        final ch = dir.sub(i, len: 1);
        if (ch == "/" or ch == "\\") { return dir.sub(0, len: i); }
        i = i - 1;
    }
    return "";
}

// findUp returns the nearest ancestor holding relative, or "" when there is none.
fun findUp(relative: str) > str {
    var dir = path\abs(".") catch "";
    while (dir != "") {
        final candidate = "{dir}/{relative}";
        final found = fs\isFile(candidate) catch false;
        if (found) { return candidate; }
        dir = parentDir(dir);
    }
    return "";
}

// pushRuleFor names the Codex prefix rule a bare push reaches, or null for anything
// else: a compound line, `git -C dir push`, an MCP call. Those reach no rule, so Codex
// would run them unprompted and no answer here may assume it prompts.
fun pushRuleFor(line: str) > str? {
    foreach (meta in SHELL_METACHARACTERS) {
        if (line.indexOf(meta) != null) { return null; }
    }
    if (line == "git push" or line.startsWith("git push ")) { return `["git","push"]`; }
    if (line == "hg push" or line.startsWith("hg push ")) { return `["hg","push"]`; }
    if (line == "sl push" or line.startsWith("sl push ")) { return `["sl","push"]`; }
    if (line == "jj git push" or line.startsWith("jj git push ")) { return `["jj","git","push"]`; }
    return null;
}

// codexPromptBlocker states why Codex will not put this call in front of the person,
// and "" when its own approval prompt will.
fun codexPromptBlocker(event: any?, pushRule: str?) > str {
    final mode = field(event, dotPath: "permission_mode");
    if (mode == "bypassPermissions" or mode == "dontAsk") {
        return "this Codex session runs in permission_mode {mode}, which never prompts";
    }
    if (pushRule == null) {
        return "no Codex approval rule matches this call, only a plain git push, hg push, sl push or jj git push command";
    }
    final rules = findUp(".codex/rules/magus.rules");
    if (rules != "") {
        // A file that is THERE and unreadable is not the same as one that carries no
        // matching rule, and collapsing the two produced a deny whose stated reason was
        // false. Named separately so the person is told what to look at.
        final body = fs\readFile(rules) catch null;
        if (body == null) {
            return "{rules} exists but could not be read, so whether it carries the prompt rule for this push is unknown";
        }
        // Compared with the spacing removed, so the rule matches however it is
        // formatted. The leading bracket is what keeps the git rule from being found
        // inside jj's ["jj", "git", "push"].
        final packed = body!.replace(" ", with: "").replace("\t", with: "");
        if (packed.indexOf(pushRule!) != null) { return ""; }
    }
    return "no .codex/rules/magus.rules carries the prompt rule for this push, which magus agent harness apply --id codex writes";
}

// shellFlags parses this script's own argv: the `magus shell` flags the entry that wired
// it declares about itself, already split into words by the host that ran the command.
//
// Parsed against SUPPORTED_FLAGS, never filtered by shape. The three answers are: a flag
// this file knows, which is used; anything else, which is REPORTED on stderr and left out;
// and no argv at all, which is the ordinary entry. There is no fourth.
//
// Reporting is the whole point. Dropping an argument silently leaves a guard judging with
// flags nobody chose and nothing anywhere saying so, which is the same failure this file
// was just rewritten to remove. Forwarding an unknown one is no better: `magus shell`
// rejects it, prints usage, exits non-zero, and the verdict is lost to a usage error.
// Saying it and carrying on is the arm that matches every other thing that can go wrong
// here (see the unavailable and could-not-judge notices): the call is not blocked for a
// misconfiguration, and the misconfiguration is not silent. A host shows a hook's stderr
// as an error notice, so the person who wrote the entry is the one who reads it.
fun shellFlags(args: [str]) > [str] {
    final flags = mut [<str>];
    foreach (word in args) {
        if (SUPPORTED_FLAGS.indexOf(word) == null) {
            // Named in quotes because an empty or whitespace argument otherwise produces
            // "unsupported argument ;", and this line is the only remedy a misconfigured
            // reader is offered.
            warn("unsupported argument \"{word}\"; this call was judged WITHOUT it. "
                + "Supported: {SUPPORTED_FLAGS.join(", ")}. Fix the hook command in your host config.");
        } else if (flags.indexOf(word) == null) {
            flags.append(word);
        }
    }
    return flags;
}

// warn names the file, because a host reports a hook's stderr with the entry's matcher at
// best and nothing at all at worst, and a reader with eight entries needs to know which.
fun warn(message: str) > void {
    io\stderr.write("magus-command.buzz: {message}\n") catch void;
}

// noticeOnce succeeds the first time family fires in this session and fails on every
// repeat.
//
// The notices it holds report a BROKEN INSTALLATION. That is a fact for the person, and
// there is nothing in it an agent can act on, so a repeat is pure noise: measured at 2,741
// firings over recent sessions, 99% of them same-session repeats of text already declined.
//
// The marker lives under TMPDIR rather than in magus's own state because this runs when
// magus is missing or too broken to judge, so it cannot ask magus for anything. Creating
// the marker is idempotent, so two concurrent tool calls race to the same harmless result.
//
// A host that reports no session id shares one marker aged out after __MAGUS_NOTICE_WINDOW
// minutes, so the first session on such a host cannot silence every session after it.
//
// family carries the SURFACE as well as the kind of failure. All six PreToolUse entries run
// this one file, so keying on the kind alone let the first surface to break consume the
// session's only notice and left the other five silent about a different binary, a
// different flag and a different reason.
fun noticeOnce(session: str, family: str) > bool {
    final tmp = envOr("TMPDIR", fallback: "/tmp");
    // Per user, because a world-writable /tmp lets anyone pre-create someone else's marker
    // and silence their guard notices permanently.
    final dir = "{tmp}/magus-guard-notices-{fileSafe(envOr("USER", fallback: "anon"))}";
    var key = session;
    if (key == "") { key = "anon"; }
    final marker = "{dir}/{fileSafe(key)}.{fileSafe(family)}";
    fs\mkdirAll(dir) catch void;
    final exists = fs\isFile(marker) catch false;
    if (exists) {
        if (session != "") { return false; }
        if (!olderThanWindow(marker)) { return false; }
    }
    fs\writeFile(marker, content: "") catch void;
    // A marker that did not land is a notice nothing can hold to one firing, and every
    // filesystem call above fails toward firing. Left alone that reinstates the 2,741
    // repeats this exists to stop, so the agent's channel stays quiet and the person is
    // told on stderr, which is theirs and which no host injects into a context window.
    if (!(fs\isFile(marker) catch false)) {
        warn("could not write {marker}, so a guard notice cannot be held to one firing "
            + "per session; reporting it here instead of in the reply");
        return false;
    }
    return true;
}

// fileSafe keeps a session id that may hold slashes or dots from becoming a path.
fun fileSafe(key: str) > str {
    final parts = mut [<str>];
    foreach (i in 0..key.len()) {
        final ch = key.sub(i, len: 1);
        final ok = (ch >= "a" and ch <= "z") or (ch >= "A" and ch <= "Z") or (ch >= "0" and ch <= "9") or ch == "-" or ch == "_";
        if (ok) { parts.append(ch); } else { parts.append("x"); }
    }
    return parts.join("");
}

fun olderThanWindow(marker: str) > bool {
    final window = envOr("__MAGUS_NOTICE_WINDOW", fallback: "120");
    final minutes = std\parseDouble(window) ?? 120.0;
    final info = fs\stat(marker) catch null;
    if (info == null) { return true; }
    final mtime = info!.mtime as? double;
    if (mtime == null) { return true; }
    return os\time() - mtime! > minutes * 60000.0;
}

// Guard carries everything one judgment call needs, so the three call sites (the
// verdict, the unattributed retry, and the stderr capture for the failure notice)
// cannot drift apart.
object Guard {
    bin: str,
    payload: str,
    flags: [str],
    response: str,
}

// The entry's own flags ride in `extra` with everything else, so the unattributed retry
// drops them too. Keeping them there meant a binary too old for `--observes-skill-loads`
// failed BOTH attempts and the spawn surface could never recover where Bash did, and a
// binary too old to accept the flag cannot observe a skill load to begin with.
fun judge(guard: Guard, extra: [str]) > proc\ExecResult !> any {
    final args = mut ["shell"];
    foreach (arg in extra) { args.append(arg); }
    args.append("-o");
    args.append("template={guard.response}");
    return proc\exec(guard.bin, args: args, opts: {
        "quiet": true,
        "allow_failure": true,
        "stdin": guard.payload,
    });
}

// failureNotice states WHICH binary went silent, what version it is, and what it
// actually said, the three facts a reader otherwise spends a session collecting.
//
// It re-runs the guard to capture stderr, which the verdict path discards. One extra
// process, only on the path that is already broken. WARN lines are dropped because a
// config the binary is too old to parse warns BEFORE it fails, and that warning is a
// symptom of the same staleness, not the error.
fun failureNotice(guard: Guard) > str {
    var version = "";
    final probe = proc\exec(guard.bin, args: ["version"], opts: {"quiet": true, "allow_failure": true}) catch null;
    if (probe != null) { version = firstLine(probe!.stdout); }
    if (version == "") { version = "version unreadable"; }

    var why = "";
    final failed = judge(guard, extra: [<str>]) catch null;
    if (failed != null) {
        foreach (line in failed!.stderr.split("\n")) {
            if (why == "" and line != "" and line.indexOf("WARN") == null) { why = line; }
        }
    }
    if (why == "") { why = "it printed no error"; }

    return "magus guard is NOT running: {guard.bin} ({version}) could not judge this command, "
        + "so its deny and advise rules are unenforced. It said: {why}. "
        + "Rebuild or update THAT binary to restore the guard.";
}

fun contextReply(text: str) > str {
    final encoded = json\stringify(text) catch `""`;
    return CONTEXT_REPLY.replace(CONTEXT_SLOT, with: encoded);
}

// noticeReply renders a notice in the dialect of the event that asked for it.
//
// A PermissionRequest reply carries a decision and no context field, so the PreToolUse
// envelope the other surfaces use is one the host rejects outright: on the single surface
// where the notice is the only thing written, it arrived unreadable. There the prose goes
// to the person on stderr and stdout carries the envelope that leaves the decision alone.
fun noticeReply(eventName: str, text: str) > str {
    if (eventName == "PermissionRequest") {
        warn(text);
        return PERMISSION_NO_DECISION;
    }
    return contextReply(text);
}

// askBranch picks the ask arm for the host that sent this event. Only a reply this
// file assembled may claim --renders-ask; see the header for why Codex never receives
// permissionDecision "ask".
fun askBranch(event: any?, codexNamed: bool, codexInferred: bool, pushRule: str?) > str {
    final declared = env\get("HOST_ASK_BRANCH") catch "";
    if (declared != "") { return declared; }
    if (!codexNamed and !codexInferred) { return ASK_CLAUDE; }
    // Only a wiring that NAMED itself Codex may render an ask as context the agent is free
    // to skip. Inferring the host from a `turn_id` key is a guess over an envelope nobody
    // schema-types, and the two ways of being wrong are not equal: the context arm turns an
    // ask into a note that is silently ignored, while the deny arm turns it into a refusal
    // the person can act on. A host that adds `turn_id` therefore costs a deny, not a pass.
    if (!codexNamed) {
        return ASK_CODEX_BLOCKED.replace(BLOCKER_SLOT,
            with: "this event looks like Codex but the wiring never said so, and only a config that sets __MAGUS_AGENT_NAME=codex is taken at its word here");
    }
    final blocker = codexPromptBlocker(event, pushRule: pushRule);
    if (blocker == "") { return ASK_CODEX_PROMPTS; }
    return ASK_CODEX_BLOCKED.replace(BLOCKER_SLOT, with: blocker);
}

fun adviseBranch() > str {
    final suppressed = env\get("__MAGUS_NO_ADVISE") catch "";
    if (suppressed != "") { return ""; }
    return envOr("HOST_ADVISE_BRANCH", fallback: ADVISE_DEFAULT);
}

// permissionResponse answers the approval request Codex raises just before its own
// prompt. No decision object leaves the prompt to the person; allow skips it, and is
// answered only for a plain push, because this event fires for every approval Codex
// asks and a pass from the guard is not the person's consent to anything else.
fun permissionResponse(pushRule: str?) > str {
    var covered = PERMISSION_NO_DECISION;
    if (pushRule != null) { covered = PERMISSION_ALLOW; }
    return PERMISSION_DENY_HEAD + PERMISSION_NO_DECISION
        + `{{else if eq .decision "pass"}}` + covered
        + `{{else if eq .decision "advise"}}` + covered
        + PERMISSION_UNKNOWN;
}

fun main(args: [str]) > void {
    final raw = io\stdin.readAll() catch null;
    final event = json\parse(raw ?? "") catch null;

    // A payload that opens like an envelope but does not parse is a TRUNCATED one, not a
    // command line. Judged as text it matches no rule and passes, so it is refused here
    // while its shape still says what it was. The prefix test is magus's own decoder's.
    if (raw == null or (event == null and raw!.startsWith(ENVELOPE_PREFIX))) {
        io\stdout.write(envOr("__MAGUS_UNREADABLE_RESPONSE", fallback: UNREADABLE_DEFAULT)) catch void;
        return;
    }

    final eventPath = envOr("HOST_EVENT_PATH", fallback: "tool_input.command");
    final sessionPath = envOr("HOST_SESSION_PATH", fallback: "session_id");
    final transcriptPath = envOr("HOST_TRANSCRIPT_PATH", fallback: "transcript_path");
    final agentName = envOr("__MAGUS_AGENT_NAME", fallback: "claude-code");
    // Overridable alongside the other dot-paths rather than fixed: a host that points
    // HOST_EVENT_PATH at a field it DOES populate for MCP calls loses the namespace test
    // otherwise, and gets that field judged as a shell command.
    final toolPath = envOr("HOST_TOOL_PATH", fallback: "tool_name");
    final rawEvent = wholeEvent(event, dotPath: eventPath, toolPath: toolPath);

    // Read BEFORE the availability check below, because the notices that check prints
    // are held to one firing per session and the session id is what keys them.
    final session = field(event, dotPath: sessionPath);
    final transcript = field(event, dotPath: transcriptPath);
    final eventName = field(event, dotPath: "hook_event_name");
    final toolName = field(event, dotPath: toolPath);

    var pushRule: str? = null;
    if (!rawEvent) { pushRule = pushRuleFor(field(event, dotPath: eventPath)); }

    // Codex is recognized by its event as well as by name, so a Codex wiring that forgot
    // __MAGUS_AGENT_NAME still never receives permissionDecision "ask", which it would run
    // unasked. turn_id is a required field of Codex's published PreToolUse input and of its
    // PermissionRequest input; no vendored Claude Code or Cursor schema names it.
    //
    // The two are kept apart rather than or-ed, because askBranch trusts them differently.
    final codexNamed = agentName == "codex";
    final codexInferred = hasKey(event, key: "turn_id");

    var response = env\get("HOST_RESPONSE") catch "";
    var rendersAsk = [<str>];
    if (response == "" and eventName == "PermissionRequest") {
        response = permissionResponse(pushRule);
        rendersAsk = ["--renders-ask"];
    } else if (response == "") {
        response = DENY_HEAD + askBranch(event, codexNamed: codexNamed, codexInferred: codexInferred, pushRule: pushRule)
            + adviseBranch() + PASS_AND_ADVISE_TAIL + UNKNOWN_DECISION;
        rendersAsk = ["--renders-ask"];
    }

    final bin = resolveBin();
    if (bin == "" or !isExecutable(bin)) {
        if (noticeOnce(session, family: "unavailable-{toolName}")) {
            io\stdout.write(envOr("__MAGUS_UNAVAILABLE_RESPONSE",
                fallback: noticeReply(eventName, text: UNAVAILABLE_TEXT))) catch void;
        }
        return;
    }

    var payload = raw!;
    if (!rawEvent) { payload = rawField(event, dotPath: eventPath) + "\n"; }
    final guard = Guard{ bin = bin, payload = payload, flags = shellFlags(args), response = response };

    // Attribution is BEST EFFORT; the verdict is not.
    //
    // --agent-name and --session postdate the current magus release, and this template is
    // downloaded and run against whatever binary a reader already has. Passing them
    // unconditionally does not degrade the guard, it BREAKS it: an older binary rejects the
    // unknown flag, prints its usage to stdout, and exits non-zero, so the host receives no
    // verdict at all and every deny and advise rule silently stops being enforced.
    //
    // A DENY exits non-zero (2) with the verdict on stdout, so retrying on a non-zero status
    // alone would judge every blocked command twice, unattributed and recorded twice in the
    // activity trail. Emptiness alone cannot tell the cases apart either, because a pass
    // renders empty on purpose. Both together can: a rejected flag prints its usage to
    // STDERR and leaves stdout empty, while any real verdict that is not a pass leaves
    // something on stdout.
    //
    // --renders-ask rides the attributed call only. A binary too old for it is too old to
    // ask, so the retry dropping it loses nothing.
    final attributed = mut [<str>];
    foreach (flag in guard.flags) { attributed.append(flag); }
    foreach (word in ["--agent-name", agentName, "--session", session, "--transcript", transcript]) {
        attributed.append(word);
    }
    foreach (flag in rendersAsk) { attributed.append(flag); }
    var result = judge(guard, extra: attributed) catch null;
    if (result == null or (result!.code != 0 and trimTrailingNewlines(result!.stdout) == "")) {
        result = judge(guard, extra: [<str>]) catch null;
    }

    // A PASS and a BROKEN GUARD both render nothing, and telling them apart is the whole
    // point of this block. A pass exits 0 with empty output because there was nothing to
    // say; a binary that cannot run, too old for `magus shell`, unable to load the
    // workspace, half-written by a concurrent build, exits non-zero with empty output, and
    // printing that as a pass silently disables every rule with nothing anywhere saying so.
    //
    // Fail OPEN either way. A guard that blocks work because it cannot judge it has its
    // priorities backwards, and an unguarded session you know about beats one you do not.
    if (result == null or (result!.code != 0 and trimTrailingNewlines(result!.stdout) == "")) {
        if (noticeOnce(session, family: "failed-{toolName}")) {
            final chosen = env\get("__MAGUS_FAILED_RESPONSE") catch "";
            if (chosen != "") {
                io\stdout.write(chosen) catch void;
            } else {
                io\stdout.write(noticeReply(eventName, text: failureNotice(guard)) + "\n") catch void;
            }
        }
        return;
    }
    io\stdout.write(trimTrailingNewlines(result!.stdout)) catch void;
}
```

### `magus-path.buzz`

The write guard, in Buzz. The deny arm, the advise arm and the ask arm are assembled exactly as its sh twin assembles them.

```buzz
// magus guard hook: judges ONE file path an agent is about to write.
//
// Companion to magus-command.buzz, wired to your host's file-editing tool
// rather than its shell tool. The Buzz port of magus-path.sh, rendering
// byte-identical replies without jq or a POSIX shell.
//
// Run it as `magus buzz -s magus-path.buzz`; see magus-command.buzz for why `-s` is
// load-bearing. It imports no magus Buzz module
// and no spell, so the workspace stays closed and the script starts in about
// 10ms instead of paying roughly 700ms per tool call.
//
// The declared-output rule here is the one guard rule that is not a heuristic:
// magus reads every target's DECLARED outputs, so a generated file is generated
// by definition and an edit to it would be overwritten by the next run.
//
// That rule ADVISES rather than blocks. magus denies only what cannot be undone;
// a hand-edited generated file is wasteful, not destructive, since regenerating
// erases it. So it explains that the edit will be overwritten and lets the agent
// correct itself, rather than treating it as unable to learn. Every rule on this
// surface says nothing on any uncertainty, no magus, no workspace, an unclaimed
// path, because an advisory fired on a guess trains the reader to ignore it.
//
// HOST_RESPONSE renders BOTH arms even though the rules shipping today only
// advise. That is deliberate, and it is why the arm predates any rule that fires it.
// These files are COPIED into a reader's config and never self-correct, so a
// deny arm added at the same time as the first denying rule would fail OPEN on
// every already-installed copy: the deny renders empty, magus exits non-zero,
// and the tail below reads empty-output-plus-nonzero as a broken guard and exits
// 0, which every host takes as allow. Shipping the arm first gives installed
// copies a window to update against a rule that is not yet firing.
//
// A host with no file-write hook still gets the command rules; it just misses
// this one. That is a coverage difference to record, not a reason to skip it.
//
// __MAGUS_AGENT_NAME and HOST_SESSION_PATH work exactly as they do in
// magus-command.buzz: attribution recorded on the activity event, never an
// input to the verdict.
//
// Every knob this file reads, each meaning what its magus-command.buzz twin means:
//
//   HOST_EVENT_PATH, HOST_SESSION_PATH, HOST_TRANSCRIPT_PATH  dot-paths into the event
//   HOST_RESPONSE, HOST_ASK_BRANCH, HOST_ADVISE_BRANCH  the reply template and its arms
//   __MAGUS_NO_ADVISE  render an advise as nothing, for a host with no context channel
//   __MAGUS_AGENT_NAME, __MAGUS_BIN  attribution, and the binary when it is not on PATH
//   __MAGUS_UNAVAILABLE_RESPONSE  what to print when magus cannot be found
//   __MAGUS_FAILED_RESPONSE  the same, for a magus found but unable to judge
//   __MAGUS_UNREADABLE_RESPONSE  what to print when the write never arrived, the one
//                    case here that denies rather than failing open
//
// NOTHING here may raise. Claude Code reads hook exit 2 as a BLOCK whose message
// comes from stderr, so an uncaught Buzz error would refuse a write with a stack
// trace as its reason. Every call that can fail is caught.
//
// Coverage declaration, machine-read by the host-parity gate; see the longer
// note in magus-command.buzz. It records what HOST_RESPONSE RENDERS, not
// which rules currently fire, so deny=model is true the moment the arm exists.
//
// No rule asks on this surface today. The arm exists for the same reason the deny arm did
// before its first rule: an installed copy never self-corrects. Claude Code prompts on it;
// Codex does not support a hook ask and no Codex rule prompts for a write, so there it
// renders as a deny.
// claude-code only, and no codex row: codex is wired to magus-path.sh, and a
// coverage declaration states what a HOST gets. The Codex ask arm is implemented and
// graded here anyway, so a Codex config can move to Buzz with no window in which a
// decision renders as nothing.
// magus-guard-template: 16
// magus-guard-coverage: schema=1 host=claude-code surface=path deny=model advise=model pass=none ask=human

import "io";
import "encoding/json" as json;
import "env";
import "fs";
import "path";
import "proc";

final ADVISE_DEFAULT = `{{else if eq .decision "advise"}}{"hookSpecificOutput":{"hookEventName":"PreToolUse","additionalContext":{{toJson .context}}}}`;

final ASK_CLAUDE = `{{else if eq .decision "ask"}}{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"ask","permissionDecisionReason":{{toJson .reason}}}}`;

final ASK_CODEX = `{{else if eq .decision "ask"}}{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":{{toJson (print .reason "\n\nThis write needs the approval of the person you work for, and Codex has no prompt for it. Ask them to make it themselves.")}}}}`;

// A decision this file does not know is refused, never allowed; see magus-command.buzz.
final UNKNOWN_DECISION = `{{else}}{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":{{toJson (print "magus guard returned the decision " .decision ", which this hook does not know, so it refuses the call rather than allow it. Update the hook template from the magus docs.")}}}}{{end}}`;

// The one arm here that does not fail open; see magus-command.buzz. The call never
// arrived, so no rule was offered it.
final UNREADABLE_DEFAULT = `{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":"magus guard could not read this write from the host, so nothing was judged. A payload that arrives truncated reads exactly like an empty one, which is why this is blocked rather than cleared. Retry the call."}}`;

final DENY_HEAD = `{{if eq .decision "deny"}}{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":{{toJson .reason}}}}`;
final PASS_AND_ADVISE_TAIL = `{{else if eq .decision "advise"}}{{else if eq .decision "pass"}}`;

// The namespace a host gives a tool served over MCP; see magus-command.buzz.
final MCP_TOOL_PREFIX = "mcp__";

// What magus's own decoder tests to tell an envelope from a bare path. `\{` escapes the
// interpolation a lone brace would open.
final ENVELOPE_PREFIX = "\{";

fun envOr(name: str, fallback: str) > str {
    final value = env\get(name) catch "";
    if (value == "") { return fallback; }
    return value;
}

fun digKey(node: any?, key: str) > any? {
    final m = node as? {str: any};
    if (m == null) { return null; }
    return m![key];
}

fun dig(event: any?, dotPath: str) > any? {
    var cur = event;
    foreach (key in dotPath.split(".")) {
        cur = digKey(cur, key: key);
        if (cur == null) { return null; }
    }
    return cur;
}

fun render(value: any) > str {
    if (value is str) { return value as str; }
    return json\stringify(value) catch "null";
}

// field is `jq -r ".<dotPath> // empty"`: absent reads as the empty string.
fun field(event: any?, dotPath: str) > str {
    final value = dig(event, dotPath: dotPath);
    if (value == null) { return ""; }
    return render(value);
}

// rawField is `jq -r ".<dotPath>"`, so an absent field reaches magus as "null".
fun rawField(event: any?, dotPath: str) > str {
    final value = dig(event, dotPath: dotPath);
    if (value == null) { return "null"; }
    return render(value);
}

// wholeEvent decides whether `magus shell` gets the whole envelope or just the string
// HOST_EVENT_PATH selects. magus reads a write target off the ENVELOPE under every `*_path`
// spelling, so selecting one dot-path here left NotebookEdit's `notebook_path` unjudged.
fun wholeEvent(event: any?, dotPath: str, toolPath: str) > bool {
    if (field(event, dotPath: toolPath).startsWith(MCP_TOOL_PREFIX)) { return true; }
    final value = dig(event, dotPath: dotPath);
    if (value == null) { return true; }
    return !(value is str);
}

fun hasKey(event: any?, key: str) > bool {
    final m = event as? {str: any};
    if (m == null) { return false; }
    return m![key] != null;
}

// trimTrailingNewlines matches shell command substitution, which drops every
// trailing newline from a captured verdict.
fun trimTrailingNewlines(s: str) > str {
    var end = s.len();
    while (end > 0 and s.sub(end - 1, len: 1) == "\n") { end = end - 1; }
    return s.sub(0, len: end);
}

// isExecutable answers "could proc\exec run this", which a directory cannot; see
// magus-command.buzz. 73 is 0111, the three execute bits, parenthesized for the reader
// because Buzz binds `&` tighter than `!=` where C and Go do the reverse.
fun isExecutable(candidate: str) > bool {
    final info = fs\stat(candidate) catch null;
    if (info == null) { return false; }
    if ((info!.is_dir as? bool) ?? false) { return false; }
    final mode = info!.mode as? int;
    if (mode == null) { return false; }
    return (mode! & 73) != 0;
}

// parentDir is the shell's ${dir%/*}: it yields the empty string at the top, which is
// what ends the walk. fs\dirname answers "/" there and would never terminate. Both
// separators, because a `/`-only split makes `C:\...` one part and the walk never runs.
fun parentDir(dir: str) > str {
    var i = dir.len() - 1;
    while (i >= 0) {
        final ch = dir.sub(i, len: 1);
        if (ch == "/" or ch == "\\") { return dir.sub(0, len: i); }
        i = i - 1;
    }
    return "";
}

// resolveBin prefers the workspace's own ./magus over PATH, found by walking UP to the
// magusfile rather than testing ./magus alone. magus-command.buzz carries the full
// reasoning and the two measurements behind it.
fun resolveBin() > str {
    final declared = env\get("__MAGUS_BIN") catch "";
    if (declared != "") { return declared; }
    var dir = path\abs(".") catch "";
    while (dir != "") {
        final marker = "{dir}/magusfile.buzz";
        final found = fs\isFile(marker) catch false;
        if (found) {
            final candidate = "{dir}/magus";
            if (isExecutable(candidate)) { return candidate; }
            break;
        }
        dir = parentDir(dir);
    }
    return proc\which("magus") catch "";
}

object Guard {
    bin: str,
    payload: str,
    response: str,
}

fun judge(guard: Guard, extra: [str]) > proc\ExecResult !> any {
    final args = mut ["shell", "--path"];
    foreach (arg in extra) { args.append(arg); }
    args.append("-o");
    args.append("template={guard.response}");
    return proc\exec(guard.bin, args: args, opts: {
        "quiet": true,
        "allow_failure": true,
        "stdin": guard.payload,
    });
}

fun adviseBranch() > str {
    final suppressed = env\get("__MAGUS_NO_ADVISE") catch "";
    if (suppressed != "") { return ""; }
    return envOr("HOST_ADVISE_BRANCH", fallback: ADVISE_DEFAULT);
}

fun main(args: [str]) > void {
    final eventPath = envOr("HOST_EVENT_PATH", fallback: "tool_input.file_path");
    final sessionPath = envOr("HOST_SESSION_PATH", fallback: "session_id");
    final transcriptPath = envOr("HOST_TRANSCRIPT_PATH", fallback: "transcript_path");
    final agentName = envOr("__MAGUS_AGENT_NAME", fallback: "claude-code");

    final bin = resolveBin();
    if (bin == "" or !isExecutable(bin)) {
        // Prints nothing by default: for most hosts an empty response means "allow".
        // Set __MAGUS_UNAVAILABLE_RESPONSE for a host that needs an explicit verdict.
        final unavailable = env\get("__MAGUS_UNAVAILABLE_RESPONSE") catch "";
        if (unavailable != "") { io\stdout.write(unavailable) catch void; }
        return;
    }

    final raw = io\stdin.readAll() catch null;
    final event = json\parse(raw ?? "") catch null;

    // A payload that opens like an envelope but does not parse is a TRUNCATED one, not a
    // path. Judged as text it matches no rule and passes; see magus-command.buzz.
    if (raw == null or (event == null and raw!.startsWith(ENVELOPE_PREFIX))) {
        io\stdout.write(envOr("__MAGUS_UNREADABLE_RESPONSE", fallback: UNREADABLE_DEFAULT)) catch void;
        return;
    }
    final session = field(event, dotPath: sessionPath);
    final transcript = field(event, dotPath: transcriptPath);

    // Codex by name or by its event's turn_id, exactly as magus-command.buzz decides
    // it. No Codex rule prompts for a write, so there an ask renders as a deny.
    final isCodex = agentName == "codex" or hasKey(event, key: "turn_id");
    var askBranch = env\get("HOST_ASK_BRANCH") catch "";
    if (askBranch == "") {
        if (isCodex) { askBranch = ASK_CODEX; } else { askBranch = ASK_CLAUDE; }
    }

    // Only a reply assembled here claims --renders-ask: a HOST_RESPONSE the reader wrote
    // gets a deny from magus rather than an ask it may render as nothing.
    var response = env\get("HOST_RESPONSE") catch "";
    var rendersAsk = [<str>];
    if (response == "") {
        response = DENY_HEAD + askBranch + adviseBranch() + PASS_AND_ADVISE_TAIL + UNKNOWN_DECISION;
        rendersAsk = ["--renders-ask"];
    }

    var payload = raw!;
    final toolPath = envOr("HOST_TOOL_PATH", fallback: "tool_name");
    if (!wholeEvent(event, dotPath: eventPath, toolPath: toolPath)) {
        payload = rawField(event, dotPath: eventPath) + "\n";
    }
    final guard = Guard{ bin = bin, payload = payload, response = response };

    // Attribution is BEST EFFORT; the verdict is not. --agent-name and --session postdate
    // the current magus release, and an older binary rejects the unknown flag outright,
    // printing usage to stdout and exiting non-zero, which leaves the host with no verdict
    // rather than an unattributed one. Try with attribution, fall back to the call this
    // script made before it existed.
    //
    // The retry tests status AND emptiness together, for the same reason as the command
    // template now that this surface can deny: a DENY exits non-zero (2) with the verdict
    // on stdout, so retrying on status alone would judge every blocked write twice.
    final attributed = mut ["--agent-name", agentName, "--session", session, "--transcript", transcript];
    foreach (flag in rendersAsk) { attributed.append(flag); }
    var result = judge(guard, extra: attributed) catch null;
    if (result == null or (result!.code != 0 and trimTrailingNewlines(result!.stdout) == "")) {
        result = judge(guard, extra: [<str>]) catch null;
    }

    // A pass and a broken guard both render nothing; see magus-command.buzz for why
    // telling them apart matters. Kept identical here so neither surface grows a behavior
    // the other lacks. The difference is only that this one has no default message,
    // because for most hosts an empty response on this surface already means "allow".
    if (result == null or (result!.code != 0 and trimTrailingNewlines(result!.stdout) == "")) {
        final failed = env\get("__MAGUS_FAILED_RESPONSE") catch "";
        if (failed != "") { io\stdout.write(failed) catch void; }
        return;
    }
    io\stdout.write(trimTrailingNewlines(result!.stdout)) catch void;
}
```

### `magus-observe.buzz`

The observer, in Buzz. It prints nothing, always exits 0, and declares no coverage, for the same reasons its sh twin does.

```buzz
// magus observe hook: records ONE path an agent reached, and judges nothing.
//
// This file is the source of truth for hosts wired to `magus buzz`. It is the
// Buzz port of magus-observe.sh and behaves identically without jq or a
// POSIX shell.
//
// Run it as `magus buzz -s magus-observe.buzz`; see magus-command.buzz for why `-s` is
// load-bearing. It imports no magus Buzz
// module and no spell: a read hook fires on every file an agent opens, so the
// 10ms start a closed workspace buys is worth more here than anywhere else.
//
// Wire it to the tools that only LOOK, your host's read equivalent. The guard
// templates beside this one handle the tools that ACT. Do not point a read tool
// at those: a read event carries a file path, so the write rules would advise
// "you are editing a declared output" at a file the agent merely opened.
// --observe is what separates the two, and only this wrapper can set it,
// because only it knows which of your host's tools look.
//
// Contract: reads the host's event as JSON on stdin, selects the path, and feeds
// it to `magus shell --observe`. It prints NOTHING and always exits 0; see the
// note on that below, which is load-bearing rather than tidy. Override any of
// the variables below:
//
//   HOST_EVENT_PATH  dot-path to the read path inside your host's event
//   HOST_SESSION_PATH  dot-path to the session id inside your host's event
//   HOST_TRANSCRIPT_PATH  dot-path to your host's own log of this session
//   __MAGUS_AGENT_NAME  the agent host name recorded alongside the observation
//   __MAGUS_BIN  path to the binary, when it is not on PATH
//
// The defaults are Claude Code's event shape, matching its two siblings. A
// different host overrides the dot-paths and passes its own __MAGUS_AGENT_NAME.
//
// NO magus-guard-coverage line, and that absence is deliberate rather than an
// oversight: a coverage declaration states how much of a VERDICT a host can
// carry on a guard surface, and this file carries no verdict on no surface. It
// never denies, never advises, and cannot change what your host does next. The
// parity gates ask that question only of artifacts that answer it.
//
// magus-guard-template: 16

// EVERY call that can fail is caught, deliberately.
//
// An unparsable event, a missing binary, a flag the binary does not know: each one
// must end as silence and exit 0. A PreToolUse hook's exit status is not advisory on
// every host. Claude Code reads exit 2 as "block this tool call" and takes the message
// from stderr, so an uncaught error here could stop the agent from reading anything at
// all. An optional record must never be able to do that.

import "io";
import "encoding/json" as json;
import "env";
import "fs";
import "path";
import "proc";

fun envOr(name: str, fallback: str) > str {
    final value = env\get(name) catch "";
    if (value == "") { return fallback; }
    return value;
}

fun digKey(node: any?, key: str) > any? {
    final m = node as? {str: any};
    if (m == null) { return null; }
    return m![key];
}

fun dig(event: any?, dotPath: str) > any? {
    var cur = event;
    foreach (key in dotPath.split(".")) {
        cur = digKey(cur, key: key);
        if (cur == null) { return null; }
    }
    return cur;
}

// field is `jq -r ".<dotPath> // empty"`: absent reads as the empty string rather
// than the literal "null".
// Only a STRING is a path. Rendering an object or a list as JSON instead recorded the
// serialized blob as the file the agent reached, which the judging surfaces treat as a
// shape they cannot read; nothing to record beats recording something untrue.
fun field(event: any?, dotPath: str) > str {
    final value = dig(event, dotPath: dotPath);
    if (value == null) { return ""; }
    return (value as? str) ?? "";
}

// isExecutable answers "could proc\exec run this", which a directory cannot; see
// magus-command.buzz. 73 is 0111, the three execute bits, parenthesized for the reader
// because Buzz binds `&` tighter than `!=` where C and Go do the reverse.
fun isExecutable(candidate: str) > bool {
    final info = fs\stat(candidate) catch null;
    if (info == null) { return false; }
    if ((info!.is_dir as? bool) ?? false) { return false; }
    final mode = info!.mode as? int;
    if (mode == null) { return false; }
    return (mode! & 73) != 0;
}

// parentDir is the shell's ${dir%/*}: it yields the empty string at the top, which is
// what ends the walk. fs\dirname answers "/" there and would never terminate. Both
// separators, because a `/`-only split makes `C:\...` one part and the walk never runs.
fun parentDir(dir: str) > str {
    var i = dir.len() - 1;
    while (i >= 0) {
        final ch = dir.sub(i, len: 1);
        if (ch == "/" or ch == "\\") { return dir.sub(0, len: i); }
        i = i - 1;
    }
    return "";
}

// resolveBin prefers the workspace's own ./magus over PATH, for the same reason its two
// siblings do, and this file needs it MORE than they do, because it is silent by design.
// An older PATH copy does not know --observe at all: it rejects the flag, prints its usage
// to a stream this script discards, and exits non-zero, so the observation is simply never
// recorded, forever, with nothing anywhere saying so. Measured 2026-08-14 in magus's own
// repository, where the wiring was correct, the binary was wrong, and the trail held 3252
// events and not one read.
fun resolveBin() > str {
    final declared = env\get("__MAGUS_BIN") catch "";
    if (declared != "") { return declared; }
    var dir = path\abs(".") catch "";
    while (dir != "") {
        final marker = "{dir}/magusfile.buzz";
        final found = fs\isFile(marker) catch false;
        if (found) {
            final candidate = "{dir}/magus";
            if (isExecutable(candidate)) { return candidate; }
            break;
        }
        dir = parentDir(dir);
    }
    return proc\which("magus") catch "";
}

fun main(args: [str]) > void {
    final eventPath = envOr("HOST_EVENT_PATH", fallback: "tool_input.file_path");
    final sessionPath = envOr("HOST_SESSION_PATH", fallback: "session_id");
    final transcriptPath = envOr("HOST_TRANSCRIPT_PATH", fallback: "transcript_path");
    final agentName = envOr("__MAGUS_AGENT_NAME", fallback: "claude-code");

    // An absent observer is SILENT, where an absent guard is loud.
    //
    // The guard templates announce themselves when magus cannot be found, because an
    // unenforced deny rule is a safety fact the reader needs. Nothing is unenforced
    // here, there is no rule, so the same announcement would be a per-read interruption
    // reporting that an optional record was not written.
    final bin = resolveBin();
    if (bin == "" or !isExecutable(bin)) { return; }

    final raw = io\stdin.readAll() catch "";
    final event = json\parse(raw) catch null;

    // The path is extracted rather than forwarding the whole event, because a payload
    // magus does not recognize as an envelope is judged as the literal text it is, and
    // for a search that carries a pattern but no path that would record the entire
    // event, query text included, as the thing the agent reached.
    //
    // Nothing to record is not a failure: a host event that names no path has no reach
    // to report, and inventing one would claim a file the host never named.
    final reached = field(event, dotPath: eventPath);
    if (reached == "") { return; }

    // A magus too old for --observe rejects the flag and exits non-zero. That is a real
    // state worth knowing about once, but not once per read, so it is reported through
    // the trail's own absence rather than through the session: if reads are missing from
    // `magus session`, the binary is too old.
    proc\exec(bin, args: [
        "shell", "--observe",
        "--agent-name", agentName,
        "--session", field(event, dotPath: sessionPath),
        "--transcript", field(event, dotPath: transcriptPath),
        "--event", "PreToolUse",
    ], opts: {"quiet": true, "allow_failure": true, "stdin": reached}) catch void;
}
```

### `magus-checkpoint.buzz`

The stop recorder, in Buzz. It forwards the event whole, exactly as its sh twin
does, so it selects nothing and imports no JSON reader at all.

```buzz
// magus checkpoint hook: records where the work stands when a session stops.
//
// This file is the source of truth for hosts wired to `magus buzz`. It is the
// Buzz port of magus-checkpoint.sh and behaves identically without a POSIX
// shell, so the wiring works unchanged on Windows. The shell copy stays for the
// hosts still wired to `sh`.
//
// Run it as `magus buzz -s magus-checkpoint.buzz`; see magus-command.buzz for why `-s`
// is load-bearing. It imports no magus Buzz module
// and no spell, which is what keeps the workspace CLOSED: a Stop hook fires once
// per session rather than once per tool call, so the 700ms an open costs would
// be affordable here - and it is still refused, because the rule that keeps the
// glue closed is worth more than the one exception that would erode it.
//
// Wire it to your host's stop or session-end event. It records the revision,
// branch and dirtiness of the tree, plus your host's session id and transcript
// path as opaque pointers, so that whoever comes back to this repository - you
// tomorrow, or another session - reads `magus session` instead of reconstructing
// where the work stopped. That reconstruction is the cost this exists to remove:
// it was measured at a session id passed by hand, a guessed transcript location,
// and three failed commands before it emerged the work had never been pushed.
//
// Contract: pipes your host's event, unread, into `magus session checkpoint`.
// magus takes the two pointers only a host knows out of the envelope and ignores
// the rest; nothing in the payload becomes the note, because a note is a sentence
// a person writes. It prints NOTHING and always exits 0. Override:
//
//   __MAGUS_AGENT_NAME  the agent host name recorded alongside the checkpoint
//   __MAGUS_BIN   path to the binary, when it is not on PATH
//
// A host whose envelope spells those fields differently passes them as flags
// instead - `--session` and `--transcript` outrank the envelope - and a host that
// cannot supply either still records a usable checkpoint, because the part that
// matters is read from the tree rather than from the event.
//
// There is no JSON import here, unlike its judging siblings. This wrapper selects
// nothing: magus parses the envelope itself, so the event is forwarded whole.
//
// NO magus-guard-coverage line, for the same reason magus-observe.buzz has
// none: a coverage declaration states how much of a VERDICT a host can carry, and
// this file carries no verdict on no surface. It never denies, never advises, and
// cannot change what your host does next.
//
// magus-guard-template: 16

// EVERY call that can fail is caught, matching the templates beside it and the
// missing `set -e` in the sh copy. A hook that can fail is a hook that can break
// the session it was meant to observe, and a record of where the work stopped is
// worth strictly less than the work.

import "io";
import "env";
import "fs";
import "path";
import "proc";

fun envOr(name: str, fallback: str) > str {
    final value = env\get(name) catch "";
    if (value == "") { return fallback; }
    return value;
}

// isExecutable answers "could proc\exec run this", which a directory cannot; see
// magus-command.buzz. 73 is 0111, the three execute bits, parenthesized for the reader
// because Buzz binds `&` tighter than `!=` where C and Go do the reverse.
fun isExecutable(candidate: str) > bool {
    final info = fs\stat(candidate) catch null;
    if (info == null) { return false; }
    if ((info!.is_dir as? bool) ?? false) { return false; }
    final mode = info!.mode as? int;
    if (mode == null) { return false; }
    return (mode! & 73) != 0;
}

// parentDir is the shell's ${dir%/*}: it yields the empty string at the top, which is
// what ends the walk. fs\dirname answers "/" there and would never terminate. Both
// separators, because a `/`-only split makes `C:\...` one part and the walk never runs.
fun parentDir(dir: str) > str {
    var i = dir.len() - 1;
    while (i >= 0) {
        final ch = dir.sub(i, len: 1);
        if (ch == "/" or ch == "\\") { return dir.sub(0, len: i); }
        i = i - 1;
    }
    return "";
}

// resolveBin prefers the workspace's own ./magus over PATH, found by walking UP to the
// magusfile: a hook runs in the host's session directory, which is not always the
// workspace root. magus-command.buzz carries the full reasoning.
fun resolveBin() > str {
    final declared = env\get("__MAGUS_BIN") catch "";
    if (declared != "") { return declared; }
    var dir = path\abs(".") catch "";
    while (dir != "") {
        final marker = "{dir}/magusfile.buzz";
        final found = fs\isFile(marker) catch false;
        if (found) {
            final candidate = "{dir}/magus";
            if (isExecutable(candidate)) { return candidate; }
            break;
        }
        dir = parentDir(dir);
    }
    return proc\which("magus") catch "";
}

fun main(args: [str]) > void {
    final agentName = envOr("__MAGUS_AGENT_NAME", fallback: "claude-code");

    // An absent recorder is SILENT, where an absent guard is loud. Nothing here is
    // unenforced - there is no rule - so announcing it would interrupt the end of
    // every session to report that an optional record was not written.
    //
    // Checked before stdin is touched, exactly as the sh copy leaves the event unread
    // on this arm: there is nobody to forward it to.
    final bin = resolveBin();
    if (bin == "" or !isExecutable(bin)) { return; }

    final event = io\stdin.readAll() catch "";

    // Both streams are discarded by `quiet`: a magus too old for `session checkpoint`
    // prints its usage, and that would otherwise reach the host as this hook's response
    // every time a session ends. The absence shows up where it is actionable instead -
    // as an empty checkpoint list in `magus session`.
    proc\exec(bin, args: [
        "session", "checkpoint",
        "--agent-name", agentName,
    ], opts: {"quiet": true, "allow_failure": true, "stdin": event}) catch void;
}
```

### `magus-rehydrate.buzz`

The post-compaction brief, in Buzz. It needs neither `tr` nor `sed`: the JSON
escape its sh twin builds out of a pipeline is one byte-indexed loop here.

```buzz
// magus rehydrate hook: prints where this checkout stands, for a session that has
// lost its history.
//
// This file is the source of truth for hosts wired to `magus buzz`. It is the Buzz
// port of magus-rehydrate.sh and prints byte-identical text without a POSIX shell,
// so it needs neither `tr` nor `sed` and runs unchanged on Windows. The shell copy
// stays for the hosts still wired to `sh`.
//
// Run it as `magus buzz -s magus-rehydrate.buzz`; see magus-command.buzz for why `-s`
// is load-bearing. It imports no magus Buzz module and
// no spell: the workspace stays CLOSED, which is what lets a session-start hook add
// its block in about 10ms rather than paying roughly 700ms to open one.
//
// Wire it to your host's session-start event, for the compaction and resume cases
// at least. When a host replaces a long session's history with a summary, the model
// keeps working from prose: the branch it is on, what it has already changed, and
// which rules it agreed to all survive only as somebody's retelling, and each
// retelling is a copy of a copy. Whatever this prints lands in that context window
// instead, read off the disk at the moment it prints.
//
// Contract: runs `magus session --brief`, prints what it says, and adds one line
// naming your host's own instruction file. It judges nothing, reads no event, and
// exits 0 whatever happens. Override:
//
//   __MAGUS_BIN   path to the binary, when it is not on PATH
//   REHYDRATE_RULES   your host's instruction file, relative to the workspace root
//   REHYDRATE_FORMAT  set it to `json` for a host that reads stdout as a reply
//
// Two channels, because hosts disagree about what a session-start hook's stdout
// IS. Some add plain stdout to the model's context, which is the default here.
// Others parse stdout as a JSON reply and drop anything that is not one, so the
// same text has to arrive as a string field:
//
//   {"hookSpecificOutput":{"hookEventName":"SessionStart","additionalContext":"..."}}
//
// The escape below is written out rather than handed to json\stringify, and that is
// deliberate: the sh copy escapes with `tr` and `sed`, the two forms are graded byte
// for byte against the same brief, and a stringify that ever disagreed about a tab
// or a control character would move this file's output rather than reveal the
// difference. The loop is the sh pipeline, transcribed.
//
// The rules line is the one host-shaped part, which is why it is a variable rather
// than something magus prints: magus names the files it ships and can see
// (AGENTS.md, the installed skill directories), and the file YOUR host reads is
// yours to name. It prints only when that file is really there.
//
// NO magus-guard-coverage line, for the same reason magus-checkpoint.buzz has none: a
// coverage declaration states how much of a VERDICT a host can carry, and this file
// carries no verdict on no surface. It never denies, never advises, and cannot
// change what your host does next.
//
// magus-guard-template: 16

// EVERY call that can fail is caught, matching the templates beside it and the
// missing `set -e` in the sh copy. A hook that can fail is a hook that can break the
// session it was meant to help.

import "io";
import "env";
import "fs";
import "path";
import "proc";

// JSON_REPLY is the sh copy's printf format, %s and all, written as ONE balanced template
// rather than concatenated around the body for a lexer reason worth knowing. A backtick
// string DOES interpolate: a `{...}` run is evaluated when its contents parse as a Buzz
// expression and stays literal only when they do not. An unbalanced fragment therefore
// swallows its own closing backtick and everything to the next one, silently.
final JSON_REPLY = `{"hookSpecificOutput":{"hookEventName":"SessionStart","additionalContext":"%s"}}`;

fun envOr(name: str, fallback: str) > str {
    final value = env\get(name) catch "";
    if (value == "") { return fallback; }
    return value;
}

// isExecutable answers "could proc\exec run this", which a directory cannot; see
// magus-command.buzz. 73 is 0111, the three execute bits, parenthesized for the reader
// because Buzz binds `&` tighter than `!=` where C and Go do the reverse.
fun isExecutable(candidate: str) > bool {
    final info = fs\stat(candidate) catch null;
    if (info == null) { return false; }
    if ((info!.is_dir as? bool) ?? false) { return false; }
    final mode = info!.mode as? int;
    if (mode == null) { return false; }
    return (mode! & 73) != 0;
}

// parentDir is the shell's ${dir%/*}: it yields the empty string at the top, which is
// what ends the walk. fs\dirname answers "/" there and would never terminate. Both
// separators, because a `/`-only split makes `C:\...` one part and the walk never runs.
fun parentDir(dir: str) > str {
    var i = dir.len() - 1;
    while (i >= 0) {
        final ch = dir.sub(i, len: 1);
        if (ch == "/" or ch == "\\") { return dir.sub(0, len: i); }
        i = i - 1;
    }
    return "";
}

// workspaceRoot walks UP to the magusfile: a hook runs in the host's session
// directory, which is not always the workspace root. The walk is UNCONDITIONAL, unlike
// the one in its judging siblings, because the root is also what the rules line is
// resolved against; an explicit __MAGUS_BIN settles the binary, not the tree.
fun workspaceRoot() > str {
    var dir = path\abs(".") catch "";
    while (dir != "") {
        final found = fs\isFile("{dir}/magusfile.buzz") catch false;
        if (found) { return dir; }
        dir = parentDir(dir);
    }
    return "";
}

// resolveBin prefers the workspace's own ./magus over PATH. magus-command.buzz
// carries the full reasoning for that preference.
fun resolveBin(root: str) > str {
    final declared = env\get("__MAGUS_BIN") catch "";
    if (declared != "") { return declared; }
    if (root != "") {
        final candidate = "{root}/magus";
        if (isExecutable(candidate)) { return candidate; }
    }
    return proc\which("magus") catch "";
}

// trimTrailingNewlines matches shell command substitution, which drops every trailing
// newline from a captured brief.
fun trimTrailingNewlines(s: str) > str {
    var end = s.len();
    while (end > 0 and s.sub(end - 1, len: 1) == "\n") { end = end - 1; }
    return s.sub(0, len: end);
}

// escapeJSON is `tr '\001-\011\013-\037' '[ *]' | sed -e 's/\\/\\\\/g' -e 's/"/\\"/g'`
// followed by the read loop that joins the lines, transcribed byte for byte.
//
// Every control character but the line break becomes a space (a raw one is not legal
// inside a JSON string), backslash and quote are escaped, and each line break becomes
// the two characters a JSON string spells it with. Byte-indexed on purpose: a
// multi-byte rune's continuation bytes are all above 0x7f, so they fall through
// untouched and reassemble exactly as they arrived.
fun escapeJSON(body: str) > str {
    final buf = mut [<str>];
    foreach (i in 0..body.len()) {
        // -1 on a read that cannot happen inside this loop's bounds, chosen because 0 is a
        // real byte: the sh copy's `tr` range starts at \001 and let a NUL through, and a
        // NUL emitted raw makes the reply illegal JSON, so the host drops it whole.
        final b = body.byte(i) catch -1;
        if (b == 10) {
            buf.append("\\n");
        } else if (b < 32) {
            buf.append(" ");
        } else if (b == 92) {
            buf.append("\\\\");
        } else if (b == 34) {
            buf.append("\\\"");
        } else {
            buf.append(body.sub(i, len: 1));
        }
    }
    return buf.join("");
}

fun main(args: [str]) > void {
    final rules = envOr("REHYDRATE_RULES", fallback: "CLAUDE.md");
    final root = workspaceRoot();

    // An absent magus is SILENT, where an absent guard is loud. Nothing here is
    // unenforced (there is no rule), so announcing it would open every compacted
    // session with a report that an optional context block was not written.
    final bin = resolveBin(root);
    if (bin == "" or !isExecutable(bin)) { return; }

    // Captured rather than streamed, because the json arm has to wrap it. stderr is
    // discarded by `quiet`: a magus too old for `session --brief` prints its usage
    // there, and that would otherwise be injected as this hook's answer.
    final result = proc\exec(bin, args: ["session", "--brief"], opts: {
        "quiet": true,
        "allow_failure": true,
    }) catch null;
    if (result == null) { return; }

    var brief = trimTrailingNewlines(result!.stdout);

    // Nothing from magus is nothing to say, in either channel. The rules line trails
    // the brief and points back at it, so on its own it is a sentence about a block
    // that was never written, and an envelope carrying only that is worse than none.
    if (brief == "") { return; }

    if (root != "") {
        final hasRules = fs\isFile("{root}/{rules}") catch false;
        if (hasRules) {
            brief = brief + "\nstanding rules: " + rules + "; re-read it, the summary above is not it";
        }
    }

    if (envOr("REHYDRATE_FORMAT", fallback: "") == "json") {
        io\stdout.write(JSON_REPLY.replace("%s", with: escapeJSON(brief + "\n"))) catch void;
        return;
    }

    io\stdout.write(brief + "\n") catch void;
}
```

## `magus-checkpoint.sh`

The second template that carries no verdict. Wire it to your host's stop or
session-end event and it records where the work stands: the revision, branch and
dirtiness of the tree, plus your host's session id and transcript path as opaque
pointers. `magus session` lists what it wrote.

It is worth wiring for the case nobody plans for. A session that ends because it
finished leaves a commit; one that ends because a usage limit was reached, or
because the laptop closed, leaves the work exactly where it was and nothing
saying so. Recovering one such stop was measured at a session id passed by hand,
a guessed transcript location, and three failed commands before it emerged the
commits had never been pushed.

It records the same position [`magus vcs checkpoint`](../../../reference/manpage/magus-vcs.md)
computes and prints, and keeps it. What makes one worth keeping is the note, so
`magus session checkpoint --note "..."` is the form a person runs; the hook
records the position and the pointers, and leaves the prose alone. Nothing in the
host's payload becomes the note.

There is no `jq` here, unlike its siblings: magus parses the envelope itself, so
a machine without jq records a checkpoint rather than silently recording none. A
host that spells those fields differently passes `--session` and `--transcript`
instead, and one that can supply neither still records a usable checkpoint,
because the part that matters is read from the tree.

It declares no `magus-guard-coverage` line, for the reason
`magus-observe.sh` declares none: it carries no verdict on any surface.

```sh
#!/usr/bin/env sh
# magus checkpoint hook: records where the work stands when a session stops.
#
# This file is the source of truth. The docs site embeds it, magus's own
# repository invokes it, and you can download it and do the same. POSIX sh, no
# bashisms; nothing in it is magus-internal.
#
# Wire it to your host's stop or session-end event. It records the revision,
# branch and dirtiness of the tree, plus your host's session id and transcript
# path as opaque pointers, so that whoever comes back to this repository - you
# tomorrow, or another session - reads `magus session` instead of reconstructing
# where the work stopped. That reconstruction is the cost this exists to remove:
# it was measured at a session id passed by hand, a guessed transcript location,
# and three failed commands before it emerged the work had never been pushed.
#
# Contract: pipes your host's event, unread, into `magus session checkpoint`.
# magus takes the two pointers only a host knows out of the envelope and ignores
# the rest; nothing in the payload becomes the note, because a note is a sentence
# a person writes. It prints NOTHING and always exits 0. Override:
#
#   __MAGUS_AGENT_NAME  the agent host name recorded alongside the checkpoint
#   __MAGUS_BIN   path to the binary, when it is not on PATH
#
# A host whose envelope spells those fields differently passes them as flags
# instead - `--session` and `--transcript` outrank the envelope - and a host that
# cannot supply either still records a usable checkpoint, because the part that
# matters is read from the tree rather than from the event.
#
# There is no jq here, unlike its siblings. This wrapper selects nothing: magus
# parses the envelope itself, so a machine without jq records a checkpoint rather
# than silently recording none.
#
# NO magus-guard-coverage line, for the same reason magus-observe.sh has
# none: a coverage declaration states how much of a VERDICT a host can carry, and
# this file carries no verdict on no surface. It never denies, never advises, and
# cannot change what your host does next.
#
# magus-guard-template: 16

# NO `set -e`, deliberately, matching every template beside it. A hook that can
# fail is a hook that can break the session it was meant to observe, and a record
# of where the work stopped is worth strictly less than the work.

[ -n "$__MAGUS_AGENT_NAME" ] || __MAGUS_AGENT_NAME='claude-code'
# Prefer the workspace's own ./magus over PATH, found by walking UP to the
# magusfile: a hook runs in the host's session directory, which is not always the
# workspace root. The command template carries the full reasoning.
guard_root=$PWD
while [ -n "$guard_root" ] && [ -z "$__MAGUS_BIN" ]; do
  if [ -f "$guard_root/magusfile.buzz" ]; then
    [ -x "$guard_root/magus" ] && __MAGUS_BIN=$guard_root/magus
    break
  fi
  guard_root=${guard_root%/*}
done
[ -n "$__MAGUS_BIN" ] || __MAGUS_BIN=$(command -v magus 2>/dev/null)

# An absent recorder is SILENT, where an absent guard is loud. Nothing here is
# unenforced - there is no rule - so announcing it would interrupt the end of
# every session to report that an optional record was not written.
if [ -z "$__MAGUS_BIN" ] || [ ! -x "$__MAGUS_BIN" ]; then
  exit 0
fi

# Both streams are discarded: a magus too old for `session checkpoint` prints its
# usage, and that would otherwise reach the host as this hook's response every
# time a session ends. The absence shows up where it is actionable instead - as
# an empty checkpoint list in `magus session`.
"$__MAGUS_BIN" session checkpoint --agent-name "$__MAGUS_AGENT_NAME" >/dev/null 2>&1

exit 0
```

## `magus-rehydrate.sh`

The third template that carries no verdict. Wire it to your host's session-start
event, at least for the compaction and resume cases, and every time a host
replaces a session's history with a summary the model is handed this checkout's
state instead of a retelling: branch and revision, commits not yet on the base
ref, the dirty tree split into sources, generated outputs and paths nothing
claims, the live leases with the command that binds each one, the last recorded
run's failures with the ref that holds their output, whether anything here runs
the guard, and where the rules live.

Every line of it is read off the disk at the moment it prints, which is the
property that makes it worth wiring. A summary degrades with each retelling and
nothing in the transcript says by how much; a fact read from the tree cannot
degrade at all. Nothing in it is remembered between sessions, and none of it
comes from the host's event, which is why the hook needs no `jq` and reads no
payload.

It restates no rule either. `magus session --brief` names the files this
workspace's rules live in (AGENTS.md and the installed skill directories, when
they exist), and the template adds one line for your host's own instruction file,
`REHYDRATE_RULES`, which defaults to `CLAUDE.md` and prints only when that file
is really there. A rule copied into a hook's output is a second copy to go stale,
and the model can read the first.

Run `magus session --brief` yourself to see exactly what a session will be
handed; `-o json` is the same brief for a wrapper that wants to reshape it.

Hosts disagree about what a session-start hook's stdout IS, so this template has
two channels. Plain text is the default, for a host that adds stdout to the
model's context. `REHYDRATE_FORMAT=json` wraps the same text in
`{"hookSpecificOutput":{"hookEventName":"SessionStart","additionalContext":...}}`
for a host that parses stdout as a reply and drops anything that is not one.
There is still no `jq`: the escaping is done in the template, so a machine without
it gets its checkout back like any other.

It declares no `magus-guard-coverage` line, for the reason
`magus-observe.sh` declares none: it carries no verdict on any surface.

```sh
#!/usr/bin/env sh
# magus rehydrate hook: prints where this checkout stands, for a session that has
# lost its history.
#
# This file is the source of truth. The docs site embeds it, and you can download
# it and wire it yourself. POSIX sh, no bashisms; nothing in it is magus-internal.
#
# Wire it to your host's session-start event, for the compaction and resume cases
# at least. When a host replaces a long session's history with a summary, the model
# keeps working from prose: the branch it is on, what it has already changed, and
# which rules it agreed to all survive only as somebody's retelling, and each
# retelling is a copy of a copy. Whatever this prints lands in that context window
# instead, read off the disk at the moment it prints.
#
# Contract: runs `magus session --brief`, prints what it says, and adds one line
# naming your host's own instruction file. It judges nothing, reads no event, and
# exits 0 whatever happens. Override:
#
#   __MAGUS_BIN   path to the binary, when it is not on PATH
#   REHYDRATE_RULES   your host's instruction file, relative to the workspace root
#   REHYDRATE_FORMAT  set it to `json` for a host that reads stdout as a reply
#
# Two channels, because hosts disagree about what a session-start hook's stdout
# IS. Some add plain stdout to the model's context, which is the default here.
# Others parse stdout as a JSON reply and drop anything that is not one, so the
# same text has to arrive as a string field:
#
#   {"hookSpecificOutput":{"hookEventName":"SessionStart","additionalContext":"..."}}
#
# No jq, unlike this file's judging siblings. They need it to SELECT out of an
# event; there is nothing to select here, only text to escape, and a machine
# without jq should still get its checkout handed back.
#
# The rules line is the one host-shaped part, which is why it is a variable rather
# than something magus prints: magus names the files it ships and can see
# (AGENTS.md, the installed skill directories), and the file YOUR host reads is
# yours to name. It prints only when that file is really there.
#
# NO magus-guard-coverage line, for the same reason magus-checkpoint.sh has none: a
# coverage declaration states how much of a VERDICT a host can carry, and this file
# carries no verdict on no surface. It never denies, never advises, and cannot
# change what your host does next.
#
# magus-guard-template: 16

# NO `set -e`, deliberately, matching every template beside it. A hook that can
# fail is a hook that can break the session it was meant to help.

[ -n "$REHYDRATE_RULES" ] || REHYDRATE_RULES='CLAUDE.md'

# Walk UP to the magusfile: a hook runs in the host's session directory, which is
# not always the workspace root. The walk is unconditional, because the root is
# also what the rules line is resolved against. The command template carries the
# full reasoning for preferring the workspace's own ./magus over PATH.
guard_root=$PWD
while [ -n "$guard_root" ] && [ ! -f "$guard_root/magusfile.buzz" ]; do
  guard_root=${guard_root%/*}
done
if [ -n "$guard_root" ] && [ -z "$__MAGUS_BIN" ] && [ -x "$guard_root/magus" ]; then
  __MAGUS_BIN=$guard_root/magus
fi
[ -n "$__MAGUS_BIN" ] || __MAGUS_BIN=$(command -v magus 2>/dev/null)

# An absent magus is SILENT, where an absent guard is loud. Nothing here is
# unenforced (there is no rule), so announcing it would open every compacted
# session with a report that an optional context block was not written.
if [ -z "$__MAGUS_BIN" ] || [ ! -x "$__MAGUS_BIN" ]; then
  exit 0
fi

# JSON-escapes stdin into one string body: every control character but the line
# break becomes a space (a raw one is not legal inside a JSON string), backslash
# and quote are escaped, and the lines are joined with a literal \n.
#
# The read loop rather than a one-liner because the join has to survive a body
# whose last line carries no newline; that line is what `read` reports as a
# failure with the text still in hand.
rehydrate_escape() {
  tr '\001-\011\013-\037' '[ *]' | sed -e 's/\\/\\\\/g' -e 's/"/\\"/g' | {
    escaped=''
    while IFS= read -r line; do
      escaped=$escaped$line'\n'
    done
    [ -z "$line" ] || escaped=$escaped$line
    printf '%s' "$escaped"
  }
}

# Captured rather than streamed, because the json arm has to wrap it. stderr is
# discarded: a magus too old for `session --brief` prints its usage there, and
# that would otherwise be injected as this hook's answer.
brief=$("$__MAGUS_BIN" session --brief 2>/dev/null)

# Nothing from magus is nothing to say, in either channel. The rules line trails
# the brief and points back at it, so on its own it is a sentence about a block
# that was never written, and an envelope carrying only that is worse than none.
[ -n "$brief" ] || exit 0

if [ -n "$guard_root" ] && [ -f "$guard_root/$REHYDRATE_RULES" ]; then
  brief=$(printf '%s\nstanding rules: %s; re-read it, the summary above is not it' \
    "$brief" "$REHYDRATE_RULES")
fi

if [ "$REHYDRATE_FORMAT" = 'json' ]; then
  printf '{"hookSpecificOutput":{"hookEventName":"SessionStart","additionalContext":"%s"}}' \
    "$(printf '%s\n' "$brief" | rehydrate_escape)"
  exit 0
fi

printf '%s\n' "$brief"

exit 0
```

## Trying a verdict by hand

```sh
printf '%s' 'git stash' | magus session hook -o name
printf '%s' 'MAGUS.md' | magus session hook --path -o name
magus session hook -o template
```

The last one lists the fields available to `-o template`. A deny exits 2 with
the verdict on stdout; a pass and an advise exit 0. See [The guard](guard.md)
for the rules behind the verdicts.
