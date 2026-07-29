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
# Two Cursor facts shape the behaviour:
#
#   - A denial carries BOTH user_message (shown to you) and agent_message (sent
#     to the model); neither is delivered on an allow, so `advise` collapses to a
#     plain allow here. Those nudges live in the installed skills instead.
#   - There is NO pre-write file hook, and it does not matter: magus advises on
#     generated files rather than blocking them, so reporting after the write is
#     the intended behavior everywhere, not a Cursor concession. What Cursor
#     shaped is only the CHANNEL - stderr prose here, injected context elsewhere.

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
	verdict=$(printf '%s' "$event" | "$GUARD_MAGUS_BIN" agent hook --path --from-json file_path -o name 2>/dev/null)
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
# otherwise would give false assurance. For strict behaviour, set failClosed on
# the hook and change this to a deny.
if [ -z "$GUARD_MAGUS_BIN" ] || [ ! -x "$GUARD_MAGUS_BIN" ]; then
	printf '%s' '{"permission":"allow"}'
	exit 0
fi

printf '%s' "$event" | "$GUARD_MAGUS_BIN" agent hook --from-json command \
	-o 'template={{if eq .decision "deny"}}{"permission":"deny","user_message":{{toJson .reason}},"agent_message":{{toJson .reason}}}{{else}}{"permission":"allow"}{{end}}'
