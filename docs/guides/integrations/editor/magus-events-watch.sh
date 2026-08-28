#!/usr/bin/env sh
# The smallest useful magus event subscriber: print every target failure as it
# happens, anywhere in the workspace.
#
# This is the whole contract. `magus events` writes one JSON object per line;
# you read lines and switch on .type. There is nothing else to learn - every
# other integration in this directory is this loop plus a way to draw on a
# screen.
#
# POSIX sh, no bashisms. jq does the parsing, the same way the agent guard
# templates in ../agents/ use it.
#
#   ./magus-events-watch.sh              # watch the workspace you are in
#   ./magus-events-watch.sh /path/repo   # watch another one
#
# magus-events-schema: 1
# magus-events-types: target.result run.finished

set -eu

root=${1:-}
# --limit 0 replays nothing. A notifier that announces yesterday's failure the
# moment you start it is worse than one that stays quiet, so history is skipped
# and only what happens from now on is reported.
set -- events --follow --limit 0 --type target.result,run.finished
[ -n "$root" ] && set -- --root "$root" "$@"

# Prefer the workspace's own ./magus over PATH: a repository that builds magus
# pins its behavior in that binary, and an older PATH copy may not know this
# subcommand. Same reasoning as the guard templates.
[ -n "${MAGUS_BIN:-}" ] || { [ -x ./magus ] && MAGUS_BIN=./magus; }
[ -n "${MAGUS_BIN:-}" ] || MAGUS_BIN=$(command -v magus 2>/dev/null) || {
	echo "magus not found; set MAGUS_BIN" >&2
	exit 127
}

command -v jq >/dev/null 2>&1 || {
	echo "jq not found; this template parses with jq" >&2
	exit 127
}

# --unbuffered makes jq flush each line as it arrives. Without it a long quiet
# period holds finished events in jq's own buffer, which looks exactly like a
# build that never ran.
#
# The status file is not ceremony. A pipeline exits with its LAST stage, so a
# `magus events` that dies - a bad --type, a workspace that vanished - would
# reach jq as a clean EOF and this script would exit 0. A supervisor would see a
# healthy subscriber that had stopped subscribing. POSIX sh has no pipefail, so
# the producer records its own status and the script exits with that.
rc=$(mktemp) || exit 1
trap 'rm -f "$rc"' EXIT INT TERM

{ "$MAGUS_BIN" "$@"; echo $? >"$rc"; } | jq -r --unbuffered '
	if .type == "target.result" and .status == "failed" then
		"FAIL  \(.project):\(.target)   magus query output \(.ref // "-")"
	elif .type == "run.finished" and .status == "fail" then
		"---   run \(.inv) failed"
	else
		empty
	end
'

exit "$(cat "$rc")"
