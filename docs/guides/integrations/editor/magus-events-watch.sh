#!/usr/bin/env sh
# The smallest useful magus event subscriber: print every target failure as it
# happens, anywhere in the workspace.
#
# `magus events` streams records through its native Go template renderer, so
# this subscriber needs no JSON parser or pipeline.
#
# POSIX sh, no bashisms.
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
set -- events --follow --limit 0 --type target.result,run.finished -o 'template={{if and (eq .type "target.result") (eq .status "failed")}}FAIL  {{.project}}:{{.target}}   magus query output {{if .ref}}{{.ref}}{{else}}-{{end}}{{else if and (eq .type "run.finished") (eq .status "fail")}}---   run {{.inv}} failed{{end}}'
[ -n "$root" ] && set -- --root "$root" "$@"

# Prefer the workspace's own ./magus over PATH: a repository that builds magus
# pins its behavior in that binary, and an older PATH copy may not know this
# subcommand. Same reasoning as the guard templates.
[ -n "${MAGUS_BIN:-}" ] || { [ -x ./magus ] && MAGUS_BIN=./magus; }
[ -n "${MAGUS_BIN:-}" ] || MAGUS_BIN=$(command -v magus 2>/dev/null) || {
	echo "magus not found; set MAGUS_BIN" >&2
	exit 127
}

exec "$MAGUS_BIN" "$@"
