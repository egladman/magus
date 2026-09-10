#!/usr/bin/env bash
# pilot.sh: derive the committed pilot subset from verified.jsonl.
#
# The selection is a rule rather than a hand-picked list so it can be re-derived
# and checked: the first two instances of every repo by instance_id (flask has
# only one), then the next instances by instance_id from django and sympy until
# there are 24. Rows are kept whole, minus hints_text, which fetch.sh already
# dropped, so a pilot run needs nothing from the full table.
set -euo pipefail

HERE="$(cd "$(dirname "$0")" && pwd)"
IN="$HERE/verified.jsonl"
OUT="$HERE/pilot.jsonl"

[[ -f $IN ]] || { printf 'pilot.sh: %s is missing; run fetch.sh first\n' "$IN" >&2; exit 1; }

jq -c -s '
    (group_by(.repo) | map(sort_by(.instance_id)[:2]) | add) as $head
    | ($head | map(.instance_id)) as $taken
    | ([.[] | select(.repo == "django/django" or .repo == "sympy/sympy")
             | select([.instance_id] | inside($taken) | not)]
       | sort_by(.instance_id)) as $fill
    | ($head + $fill[:24 - ($head | length)]) | sort_by(.instance_id) | .[]
' "$IN" >"$OUT"

printf 'pilot.sh: wrote %s instances to %s\n' "$(wc -l <"$OUT" | tr -d ' ')" "$OUT"
