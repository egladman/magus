#!/usr/bin/env bash
# Runner self-test task, not part of the scored task set: it exercises seed ->
# provision -> probe -> agent -> check without needing the real fixture.
set -euo pipefail
printf '0\n' >"$1/answer.txt"
