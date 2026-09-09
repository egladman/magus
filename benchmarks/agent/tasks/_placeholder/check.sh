#!/usr/bin/env bash
set -euo pipefail
wt=$1
if [[ ! -f $wt/answer.txt ]]; then
    echo "answer.txt is missing"
    exit 1
fi
got=$(tr -d '[:space:]' <"$wt/answer.txt")
if [[ $got != 42 ]]; then
    echo "answer.txt holds '$got', want '42'"
    exit 1
fi
echo "answer.txt holds 42"
