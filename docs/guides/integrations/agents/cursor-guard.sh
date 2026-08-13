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
#   - There is NO pre-write file hook, and it does not matter: magus advises on
#     generated files rather than blocking them, so reporting after the write is
#     the intended behavior everywhere, not a Cursor concession. What Cursor
#     shaped is only the CHANNEL - stderr prose here, injected context elsewhere.
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
# magus-guard-template: 3
# magus-guard-coverage: schema=1 host=cursor surface=command deny=model advise=none pass=none
# magus-guard-coverage: schema=1 host=cursor surface=path deny=none advise=human pass=none

[ -n "$GUARD_MAGUS_BIN" ] || GUARD_MAGUS_BIN=$(command -v magus 2>/dev/null)

event=$(cat)

case "$event" in
*'"file_path"'*)
	# afterFileEdit: cannot block, so a missing magus costs a warning, not safety.
	if [ -z "$GUARD_MAGUS_BIN" ] || [ ! -x "$GUARD_MAGUS_BIN" ]; then
		exit 0
	fi
	# -o name prints the bare decision word, which is all this needs. magus
	# re-roots the absolute path Cursor sends onto the workspace itself.
    verdict=$(printf '%s' "$event" | jq -r '.file_path' | "$GUARD_MAGUS_BIN" hook --path --agent-name cursor -o name 2>/dev/null)
	[ "$verdict" = "advise" ] || exit 0
	# Cursor surfaces a non-blocking hook's stderr, so the message goes there as
	# prose rather than as a verdict it would not read.
	printf '%s\n' \
		"magus: that file is a DECLARED OUTPUT of a magus target - it is generated." \
		"The edit you just made will be overwritten by the next run of its producing target." \
		"Change the SOURCE instead, then regenerate and commit both together." \
		"Cursor has no pre-write hook, so this could only be reported after the fact." >&2
	exit 0
	;;
esac

# beforeShellExecution. Allow on a missing magus: Cursor already fails open on a
# hook crash or malformed JSON unless the hook sets failClosed, so pretending
# otherwise would give false assurance. For strict behavior, set failClosed on
# the hook and change this to a deny.
if [ -z "$GUARD_MAGUS_BIN" ] || [ ! -x "$GUARD_MAGUS_BIN" ]; then
	printf '%s' '{"permission":"allow"}'
	exit 0
fi

# Captured and printed rather than piped straight through, because `magus hook` exits
# non-zero on a deny and Cursor reads a non-zero hook as a CRASH - which it fails open on,
# unless failClosed is set. Letting that status escape would turn every block into an
# allow, silently, which is the one outcome worse than not installing the guard. Cursor's
# channel is the JSON on stdout; this exits 0 so that JSON is what it acts on.
verdict=$(printf '%s' "$event" | jq -r '.command' | "$GUARD_MAGUS_BIN" hook --agent-name cursor \
	-o 'template={{if eq .decision "deny"}}{"permission":"deny","user_message":{{toJson .reason}},"agent_message":{{toJson .reason}}}{{else}}{"permission":"allow"}{{end}}' 2>/dev/null)
[ -n "$verdict" ] || verdict='{"permission":"allow"}'
printf '%s' "$verdict"
exit 0
