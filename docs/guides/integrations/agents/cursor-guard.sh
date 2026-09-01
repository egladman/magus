#!/usr/bin/env sh
# magus guard for Cursor. ONE file, both hooks - download only this.
#
# Cursor runs a hook as a PROGRAM rather than an inline shell string, and its two
# relevant events carry different payloads, so this reads the event once and
# branches on what it finds instead of needing a wrapper per event:
#
#   beforeShellExecution  {"command": "...", "cwd": "...", "sandbox": false}
#   afterFileEdit         {"file_path": "<absolute>", "edits": [...]}
#
# Save to .cursor/hooks/cursor-guard.sh, chmod +x, and point both events at it:
#
#   {"version": 1, "hooks": {
#     "beforeShellExecution": [{"command": "./.cursor/hooks/cursor-guard.sh"}],
#     "afterFileEdit":        [{"command": "./.cursor/hooks/cursor-guard.sh"}]}}
#
# Self-contained on purpose. The other hosts' templates delegate to
# magus-guard-command.sh, but Cursor would then need three files downloaded to
# work, and a guard nobody finishes installing guards nothing.
#
# Two Cursor facts shape the behavior:
#
#   - A denial carries BOTH user_message (shown to you) and agent_message (sent
#     to the model); neither is delivered on an allow, so `advise` collapses to a
#     plain allow here. Those nudges live in the installed skills instead.
#   - There is NO pre-write file hook. For an advise that costs nothing: magus
#     advises on generated files rather than blocking them, so reporting after
#     the write is the intended behavior everywhere, and what Cursor shaped is
#     only the CHANNEL - stderr prose here, injected context elsewhere. For a
#     DENY it costs the block itself: afterFileEdit fires once the write has
#     landed, so the verdict arrives as a warning to the person and the file is
#     already changed. That is a real coverage gap, recorded as deny=human
#     below rather than papered over.
#
# Both calls pass --agent-name cursor so the observation magus records says which host
# produced it. Neither Cursor event carries a session id, so none is sent; that
# is attribution missing, not a verdict changing.
#
# Coverage declarations, machine-read by the host-parity gate - see the longer
# note in magus-guard-command.sh. Cursor is the host that loses the most, and
# the two lines say exactly where: an advise on a shell command is delivered
# nowhere (Cursor sends nothing on an allow), and an advise on a file write
# reaches the person via stderr but never the model.
# magus-guard-template: 9
# magus-guard-coverage: schema=1 host=cursor surface=command deny=model advise=none pass=none
# magus-guard-coverage: schema=1 host=cursor surface=path deny=human advise=human pass=none

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

event=$(cat)

case "$event" in
*'"file_path"'*)
	# afterFileEdit: cannot block, so a missing magus costs a warning, not safety.
	# It still SAYS so, on the same stderr channel this arm already uses for a
	# verdict. Exiting quietly here is indistinguishable from a clean edit, and an
	# unguarded session you know about beats one you do not.
	if [ -z "$GUARD_MAGUS_BIN" ] || [ ! -x "$GUARD_MAGUS_BIN" ]; then
		printf '%s\n' "magus guard is NOT running: magus is not on PATH, so its rules did not judge this edit. Install magus, or set GUARD_MAGUS_BIN to its path, to restore the guard." >&2
		exit 0
	fi
	# Ask for the MESSAGE, not the bare decision word. Several rules judge this
	# surface now, so a hardcoded explanation here would report the wrong one -
	# it used to say "that file is a DECLARED OUTPUT" whatever had actually
	# fired. The template renders the deny reason or the advise context, and
	# nothing at all for a pass. magus re-roots the absolute path Cursor sends
	# onto the workspace itself.
	verdict=$(printf '%s' "$event" | jq -r '.file_path' | "$GUARD_MAGUS_BIN" session hook --path --agent-name cursor \
		-o 'template={{if eq .decision "deny"}}{{.reason}}{{else if eq .decision "advise"}}{{.context}}{{end}}' 2>/dev/null)
	[ -n "$verdict" ] || exit 0
	# Cursor surfaces a non-blocking hook's stderr, so the message goes there as
	# prose rather than as a verdict it would not read. A deny cannot block here
	# - afterFileEdit fires after the write - so it is reported as what it is.
	printf '%s\n' "magus: $verdict" \
		"Cursor has no pre-write hook, so this could only be reported after the fact." >&2
	exit 0
	;;
esac

# beforeShellExecution. Allow on a missing magus: Cursor already fails open on a
# hook crash or malformed JSON unless the hook sets failClosed, so pretending
# otherwise would give false assurance. For strict behavior, set failClosed on
# the hook and change this to a deny.
#
# The allow is announced on stderr, which Cursor logs. Cursor delivers no message
# on an allow, so this is the only channel left, and a silent fail-open on the
# shell surface is the one outcome nobody can tell from a guarded session.
if [ -z "$GUARD_MAGUS_BIN" ] || [ ! -x "$GUARD_MAGUS_BIN" ]; then
	printf '%s\n' "magus guard is NOT running: magus is not on PATH, so its deny and advise rules are unenforced right now. Install magus, or set GUARD_MAGUS_BIN to its path, to restore the guard." >&2
	printf '%s' '{"permission":"allow"}'
	exit 0
fi

# Captured and printed rather than piped straight through, because `magus session hook` exits
# non-zero on a deny and Cursor reads a non-zero hook as a CRASH - which it fails open on,
# unless failClosed is set. Letting that status escape would turn every block into an
# allow, silently, which is the one outcome worse than not installing the guard. Cursor's
# channel is the JSON on stdout; this exits 0 so that JSON is what it acts on.
verdict=$(printf '%s' "$event" | jq -r '.command' | "$GUARD_MAGUS_BIN" session hook --agent-name cursor \
	-o 'template={{if eq .decision "deny"}}{"permission":"deny","user_message":{{toJson .reason}},"agent_message":{{toJson .reason}}}{{else}}{"permission":"allow"}{{end}}' 2>/dev/null)
# An empty verdict is a BROKEN guard, never a pass: the template above renders
# {"permission":"allow"} for every decision that is not a deny, so nothing but a
# magus that could not run leaves this empty - too old for `session hook`, unable
# to load the workspace, half-written by a concurrent build. Allowing is still
# right (Cursor fails open on a crash anyway), announcing it is what was missing.
#
# It names the evidence rather than guessing at it: which binary went silent, its version,
# and the error it printed. The wording this replaces blamed "too old, or cannot load this
# workspace", and the second half is not a cause - the deny rules need no workspace at all -
# so it sent readers looking for a problem there was never any sign of. The re-run is what
# captures the stderr the verdict call discards, and it only ever happens here, on the path
# that is already broken. WARN lines are dropped: a config too new for the binary warns
# before it fails, which is a symptom of the same staleness rather than the error.
if [ -z "$verdict" ]; then
	ver=$("$GUARD_MAGUS_BIN" version 2>/dev/null | head -n 1)
	[ -n "$ver" ] || ver='version unreadable'
	why=$(printf '%s' "$event" | jq -r '.command' | "$GUARD_MAGUS_BIN" session hook --agent-name cursor 2>&1 >/dev/null | grep -v 'WARN' | head -n 1)
	[ -n "$why" ] || why='it printed no error'
	printf 'magus guard is NOT running: %s (%s) could not judge this command, so its deny and advise rules are unenforced. It said: %s. Rebuild or update THAT binary to restore the guard.\n' \
		"$GUARD_MAGUS_BIN" "$ver" "$why" >&2
	verdict='{"permission":"allow"}'
fi
printf '%s' "$verdict"
exit 0
