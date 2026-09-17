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
# magus-guard-template: 15
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
session=$(printf '%s' "$event" | jq -r ".$HOST_SESSION_PATH // empty" 2>/dev/null)
transcript=$(printf '%s' "$event" | jq -r ".$HOST_TRANSCRIPT_PATH // empty" 2>/dev/null)
event_name=$(printf '%s' "$event" | jq -r '.hook_event_name // empty' 2>/dev/null)

# plain_push succeeds when the call is one bare `git push`, the only shape the Codex prompt
# rule matches. Anything else (a compound line, `git -C dir push`, an MCP call) reaches no
# rule, so Codex would run it unprompted, and no answer here may assume it prompts.
plain_push() {
  [ -z "$HOST_EVENT_RAW" ] || return 1
  push_line=$(printf '%s' "$event" | jq -r ".$HOST_EVENT_PATH // empty" 2>/dev/null)
  case $push_line in
  *[\;\&\|\`\$\(\)\<\>\\]* | *"
"*) return 1 ;;
  "git push" | "git push "*) return 0 ;;
  esac
  return 1
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
    printf 'no Codex approval rule matches this call, only a plain git push command'
    return
  }
  rules_dir=$PWD
  while [ -n "$rules_dir" ]; do
    if [ -f "$rules_dir/.codex/rules/magus.rules" ]; then
      grep -q '"git", *"push"' "$rules_dir/.codex/rules/magus.rules" && return
      break
    fi
    rules_dir=${rules_dir%/*}
  done
  printf 'no .codex/rules/magus.rules carries the git push prompt rule, which magus agent harness apply --id codex writes'
}

# Codex is recognized by its event as well as by name, so a Codex wiring that forgot
# __MAGUS_AGENT_NAME still never receives permissionDecision "ask", which it would run
# unasked. turn_id is a required field of Codex's published PreToolUse input
# (testdata/hosts/codex/pre-tool-use.command.input.schema.json) and of its
# PermissionRequest input; no vendored Claude Code or Cursor schema names it.
codex=
if [ "$__MAGUS_AGENT_NAME" = codex ] || [ "$(printf '%s' "$event" | jq -r 'has("turn_id")' 2>/dev/null)" = true ]; then
  codex=1
fi

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
    ask_blocker=$(codex_cannot_prompt)
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
